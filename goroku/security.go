package goroku

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

const (
	OWNER                   = 1 << 0
	SUDO                    = 1 << 1
	SUPPORT                 = 1 << 2
	GROUP_OWNER             = 1 << 3
	GROUP_ADMIN_ADD_ADMINS  = 1 << 4
	GROUP_ADMIN_CHANGE_INFO = 1 << 5
	GROUP_ADMIN_BAN_USERS   = 1 << 6
	GROUP_ADMIN_DEL_MSGS    = 1 << 7
	GROUP_ADMIN_PIN_MSGS    = 1 << 8
	GROUP_ADMIN_INVITE      = 1 << 9
	GROUP_ADMIN             = 1 << 10
	GROUP_MEMBER            = 1 << 11
	PM                      = 1 << 12
	EVERYONE                = 1 << 13

	DEFAULT_PERMISSIONS = OWNER
	ALL                 = (1 << 14) - 1
)

// privilegedModules cannot be delegated wholesale: a module-scoped tsec or
// sgroups rule on any of them used to be equivalent to handing out owner
// rights, because the commands they expose rewrite the owner/sudo/tsec lists or
// run arbitrary code on the host. A module-scoped rule on one of these now
// confers only their read-only commands; see moduleRuleGrants. Command-scoped
// rules keep working — those name exactly what they grant, so the owner cannot
// hand over more than they meant.
var privilegedModules = map[string]struct{}{
	"gorokusecurity": {},
	"gorokubackup":   {},
	"gorokuconfig":   {},
	"updater":        {},
	"loader":         {},
	"eval":           {},
	"terminal":       {},
}

// IsPrivilegedModule reports whether moduleName may not be delegated with a
// module-scoped rule. Exported so the inline manager, which cannot import this
// package's internals, applies the same list to callback-button access.
func IsPrivilegedModule(moduleName string) bool {
	_, ok := privilegedModules[strings.ToLower(strings.TrimSpace(moduleName))]
	return ok
}

// IsPrivilegedModule satisfies inline.SecurityChecker.
func (sm *SecurityManager) IsPrivilegedModule(moduleName string) bool {
	return IsPrivilegedModule(moduleName)
}

// moduleRuleGrants reports whether a module-scoped rule named ruleName may
// grant reg. It is the single place where the privileged-module deny list is
// enforced, for tsec user rules, tsec chat rules and sgroups alike.
//
// Inside a privileged module the rule stops at the module's owner-only
// commands: "let this user manage the sudo list" must not also mean "let this
// user run .owneradd and promote themselves". Read-only commands of the same
// module keep working, since they change nothing.
func moduleRuleGrants(reg *commandRegistration, ruleName string) bool {
	if !registrationInModule(reg, ruleName) {
		return false
	}
	return !IsPrivilegedModule(ruleName) || !reg.Meta.OnlyOwner
}

// delegationAllowed reports whether a matching tsec/sgroups rule may still
// grant a command whose effective mask is config. config has already been
// clipped by getBoundingMask(); when nothing survives the clip the operator has
// taken the command away from everyone but the owner, and a delegated rule must
// not hand it back — otherwise a rule grants more than the bounding mask allows.
func delegationAllowed(config int) bool {
	return config != 0
}

type SecuredModule interface {
	CommandPermissions() map[string]int
}

type SecurityGroup struct {
	Name        string           `json:"name"`
	Users       []int64          `json:"users"`
	Permissions []map[string]any `json:"permissions"`
}

type SecurityRule struct {
	Target     int64  `json:"target"`
	RuleType   string `json:"rule_type"`
	Rule       string `json:"rule"`
	Expires    int64  `json:"expires"`
	EntityName string `json:"entity_name"`
	EntityURL  string `json:"entity_url"`
}

type SecurityManager struct {
	mu                   sync.RWMutex
	client               *CustomTelegramClient
	db                   *Database
	anyAdmin             bool
	defaultMask          int
	tsecChat             *PointerList[SecurityRule]
	tsecUser             *PointerList[SecurityRule]
	owner                *PointerList[int64]
	allUsers             *PointerList[int64]
	sgroups              map[string]SecurityGroup
	rightsReloadInterval time.Duration
	stopCh               chan struct{}
	stopOnce             sync.Once
	done                 chan struct{}
	// adminCache caches per-chat/per-user admin rights lookups (5-min TTL, mirrors Python security.py)
	adminCache map[string]adminCacheEntry
}

type adminCacheEntry struct {
	result bool
	exp    int64
}

func NewSecurityManagerChecked(client *CustomTelegramClient, db *Database) (*SecurityManager, error) {
	anyAdmin := db.GetBool("goroku.security", "any_admin", false)
	defaultMask := db.GetInt("goroku.security", "default", OWNER)
	tsecChat, err := NewPointerListChecked[SecurityRule](db, "goroku.security", "tsec_chat", nil)
	if err != nil {
		return nil, err
	}
	tsecUser, err := NewPointerListChecked[SecurityRule](db, "goroku.security", "tsec_user", nil)
	if err != nil {
		return nil, err
	}
	owner, err := NewPointerListChecked[int64](db, "goroku.security", "owner", nil)
	if err != nil {
		return nil, err
	}
	allUsers, err := NewPointerListChecked[int64](db, "goroku.security", "all_users", nil)
	if err != nil {
		return nil, err
	}

	sm := &SecurityManager{
		client:               client,
		db:                   db,
		anyAdmin:             anyAdmin,
		defaultMask:          defaultMask,
		tsecChat:             tsecChat,
		tsecUser:             tsecUser,
		owner:                owner,
		allUsers:             allUsers,
		sgroups:              make(map[string]SecurityGroup),
		adminCache:           make(map[string]adminCacheEntry),
		rightsReloadInterval: time.Minute,
		stopCh:               make(chan struct{}),
		done:                 make(chan struct{}),
	}

	if err := sm.reloadRights(); err != nil {
		return nil, fmt.Errorf("persist initial security state: %w", err)
	}
	sm.startRightsReloader()
	return sm, nil
}

// NewSecurityManager preserves the original constructor contract. Failures are
// logged and produce a stopped, deny-by-default manager; production startup
// uses NewSecurityManagerChecked and propagates the failure.
func NewSecurityManager(client *CustomTelegramClient, db *Database) *SecurityManager {
	sm, err := NewSecurityManagerChecked(client, db)
	if err == nil {
		return sm
	}
	L().Error("Security state initialization failed; using deny-by-default fallback", zap.Error(err))
	sm = &SecurityManager{
		client: client, db: db, defaultMask: 0,
		tsecChat: &PointerList[SecurityRule]{db: db, module: "goroku.security", key: "tsec_chat", values: []SecurityRule{}},
		tsecUser: &PointerList[SecurityRule]{db: db, module: "goroku.security", key: "tsec_user", values: []SecurityRule{}},
		owner:    &PointerList[int64]{db: db, module: "goroku.security", key: "owner", values: []int64{}},
		allUsers: &PointerList[int64]{db: db, module: "goroku.security", key: "all_users", values: []int64{}},
		sgroups:  make(map[string]SecurityGroup), adminCache: make(map[string]adminCacheEntry),
		rightsReloadInterval: 0, stopCh: make(chan struct{}),
		done: make(chan struct{}),
	}
	close(sm.done)
	return sm
}

func (sm *SecurityManager) startRightsReloader() {
	if sm.rightsReloadInterval <= 0 {
		close(sm.done)
		return
	}

	ticker := time.NewTicker(sm.rightsReloadInterval)
	go func() {
		defer ticker.Stop()
		defer close(sm.done)
		for {
			select {
			case <-ticker.C:
				if err := sm.reloadRights(); err != nil {
					L().Error("Failed to persist periodic security reload", zap.Error(err))
				}
			case <-sm.stopCh:
				return
			}
		}
	}()
}

func (sm *SecurityManager) Stop() {
	sm.stopOnce.Do(func() { close(sm.stopCh) })
}

// Close requests worker shutdown and waits for completion.
func (sm *SecurityManager) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sm.Stop()
	select {
	case <-sm.done:
		return nil
	default:
	}
	select {
	case <-sm.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sm *SecurityManager) ReloadRights() {
	if err := sm.reloadRights(); err != nil {
		L().Error("Failed to reload security state", zap.Error(err))
	}
}

func (sm *SecurityManager) ReloadRightsErr() error {
	return sm.reloadRights()
}

func (sm *SecurityManager) reloadRights() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.reloadRightsLocked(sm.owner.ToSlice(), sm.tsecUser.ToSlice(), sm.tsecChat.ToSlice(), sm.sgroups, false)
}

func (sm *SecurityManager) reloadRightsLocked(owner []int64, userRules, chatRules []SecurityRule, groups map[string]SecurityGroup, persistGroups bool) error {
	groups = clonePointerValue(groups)
	// Ensure client owner ID is in the list of owners. Not while it is still
	// zero: an unauthorized client would seed the persisted owner list with 0,
	// which then matches every message that carries no sender (anonymous
	// admins, channel-signed posts).
	if selfID := sm.client.TGIDValue(); selfID != 0 {
		hasOwner := false
		for _, id := range owner {
			if id == selfID {
				hasOwner = true
				break
			}
		}
		if !hasOwner {
			owner = append(owner, selfID)
		}
	}

	// Clean up expired rules
	now := time.Now().Unix()
	for i := len(userRules) - 1; i >= 0; i-- {
		if userRules[i].Expires > 0 && userRules[i].Expires < now {
			userRules = append(userRules[:i], userRules[i+1:]...)
		}
	}

	for i := len(chatRules) - 1; i >= 0; i-- {
		if chatRules[i].Expires > 0 && chatRules[i].Expires < now {
			chatRules = append(chatRules[:i], chatRules[i+1:]...)
		}
	}
	// Rebuild all_users list (mirrors Python _reload_rights)
	var sgroupUsers []int64
	for _, g := range groups {
		sgroupUsers = append(sgroupUsers, g.Users...)
	}
	var tsecUsers []int64
	for _, rule := range userRules {
		tsecUsers = append(tsecUsers, rule.Target)
	}
	allUsersSet := make(map[int64]struct{})
	for _, id := range sgroupUsers {
		allUsersSet[id] = struct{}{}
	}
	for _, id := range tsecUsers {
		allUsersSet[id] = struct{}{}
	}
	for _, id := range owner {
		allUsersSet[id] = struct{}{}
	}
	var allUsersList []int64
	for id := range allUsersSet {
		allUsersList = append(allUsersList, id)
	}
	// Cleanup command_prefixes for users no longer in all_users (mirrors Python security.py:209-215)
	prefixes := sm.db.GetStringMap("goroku.main", "command_prefixes", nil)
	for idStr := range prefixes {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if _, ok := allUsersSet[id]; !ok {
			delete(prefixes, idStr)
		}
	}
	securityUpdates := map[string]any{
		"owner":     owner,
		"tsec_user": userRules,
		"tsec_chat": chatRules,
		"all_users": allUsersList,
	}
	if persistGroups {
		persistedGroups := make(map[string]any, len(groups))
		for name, group := range groups {
			persistedGroups[name] = map[string]any{
				"users":       group.Users,
				"permissions": group.Permissions,
			}
		}
		securityUpdates["sgroups"] = persistedGroups
	}
	err := sm.db.Update(map[string]map[string]any{
		"goroku.security": securityUpdates,
		"goroku.main":     {"command_prefixes": prefixes},
	})
	if err != nil && !errors.Is(err, ErrDatabaseCommitUncertain) {
		return err
	}
	ownerPointer, loadErr := NewPointerListChecked[int64](sm.db, "goroku.security", "owner", nil)
	if loadErr != nil {
		return errors.Join(err, loadErr)
	}
	userPointer, loadErr := NewPointerListChecked[SecurityRule](sm.db, "goroku.security", "tsec_user", nil)
	if loadErr != nil {
		return errors.Join(err, loadErr)
	}
	chatPointer, loadErr := NewPointerListChecked[SecurityRule](sm.db, "goroku.security", "tsec_chat", nil)
	if loadErr != nil {
		return errors.Join(err, loadErr)
	}
	allUsersPointer, loadErr := NewPointerListChecked[int64](sm.db, "goroku.security", "all_users", nil)
	if loadErr != nil {
		return errors.Join(err, loadErr)
	}
	sm.owner = ownerPointer
	sm.tsecUser = userPointer
	sm.tsecChat = chatPointer
	sm.allUsers = allUsersPointer
	sm.sgroups = groups
	return err
}

func (sm *SecurityManager) ApplySgroups(sgroups map[string]SecurityGroup) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.reloadRightsLocked(sm.owner.ToSlice(), sm.tsecUser.ToSlice(), sm.tsecChat.ToSlice(), sgroups, true)
}

func (sm *SecurityManager) Check(msg *Message, command string) bool {
	var reg *commandRegistration
	if sm.client.Loader != nil {
		reg, _ = sm.client.Loader.resolveCommand(command)
	}
	return sm.checkCommand(msg, command, reg)
}

func (sm *SecurityManager) checkRegistration(msg *Message, reg *commandRegistration) bool {
	return sm.checkCommand(msg, reg.Name, reg)
}

func (sm *SecurityManager) checkCommand(msg *Message, command string, reg *commandRegistration) bool {
	// Debug, not Info: this fires for every command from every sender, so at
	// Info it drowns the log in routine permission checks.
	L().Debug("security check",
		zap.Int64("sender_id", msg.SenderID),
		zap.Bool("out", msg.Out),
		zap.Int64("client_tgid", sm.client.TGIDValue()),
		zap.String("command", command))
	// First, if owner/client, bypass security check.
	//
	// SenderID 0 is not an identity: anonymous admins and channel-signed posts
	// carry no user, and the client TGID is also 0 until the session
	// authorizes. Comparing them matched, so during startup — or with a client
	// that never authorized — an anonymous admin's message passed as the owner.
	selfID := sm.client.TGIDValue()
	if selfID != 0 && msg.SenderID == selfID {
		return true
	}
	if msg.Out {
		return true
	}

	// Read whitelist owner IDs. A zero sender is not an identity and must not
	// match a zero entry that an older build persisted into the list.
	if msg.SenderID != 0 {
		for _, id := range sm.owner.ToSlice() {
			if id != 0 && msg.SenderID == id {
				return true
			}
		}
	}

	// Read blacklist user IDs
	for _, bid := range sm.db.GetInt64Slice("goroku.main", "blacklist_users", nil) {
		if msg.SenderID == bid {
			return false
		}
	}

	// Get mask config for the command
	config := sm.getFlagsForRegistration(command, reg)

	if (config & SUDO) != 0 {
		for _, id := range sm.db.GetInt64Slice("goroku.security", "sudo", nil) {
			if msg.SenderID == id {
				return true
			}
		}
	}

	// If everyone can access
	if (config & EVERYONE) != 0 {
		return true
	}

	// Delegated rules (tsec, sgroups) are only consulted while the bounding
	// mask still leaves the command reachable by someone other than the owner.
	if delegationAllowed(config) {
		// Check temporary tsec user rules
		for _, rule := range sm.GetUserRules() {
			if rule.Target == msg.SenderID {
				if rule.RuleType == "command" && rule.Rule == command {
					return true
				}
				// If rule is module-wide
				if rule.RuleType == "module" {
					if moduleRuleGrants(reg, rule.Rule) {
						return true
					}
				}
			}
		}

		// Check temporary tsec chat rules, mirroring Python security._tsec_chat.
		for _, rule := range sm.GetChatRules() {
			if rule.Target == msg.ChatID {
				if rule.RuleType == "command" && rule.Rule == command {
					return true
				}
				if rule.RuleType == "module" && moduleRuleGrants(reg, rule.Rule) {
					return true
				}
			}
		}

		// Check security groups (sgroups)
		sm.mu.RLock()
		for _, sgroup := range sm.sgroups {
			hasUser := false
			for _, u := range sgroup.Users {
				if u == msg.SenderID {
					hasUser = true
					break
				}
			}
			if hasUser {
				for _, perm := range sgroup.Permissions {
					ruleType, _ := perm["rule_type"].(string)
					ruleName, _ := perm["rule"].(string)
					if ruleType == "command" && ruleName == command {
						sm.mu.RUnlock()
						return true
					}
					if ruleType == "module" && moduleRuleGrants(reg, ruleName) {
						sm.mu.RUnlock()
						return true
					}
				}
			}
		}
		sm.mu.RUnlock()
	}

	// PM permission check
	if msg.IsPrivate && (config&PM) != 0 {
		return true
	}

	// Group member check
	if (msg.IsGroup || msg.IsChannel) && (config&GROUP_MEMBER) != 0 {
		return true
	}

	// Check group owner/admin permissions
	if msg.IsGroup || msg.IsChannel {
		fGroupOwner := (config & GROUP_OWNER) != 0
		fGroupAdminAny := (config & (GROUP_ADMIN | GROUP_ADMIN_ADD_ADMINS | GROUP_ADMIN_CHANGE_INFO | GROUP_ADMIN_BAN_USERS | GROUP_ADMIN_DEL_MSGS | GROUP_ADMIN_PIN_MSGS | GROUP_ADMIN_INVITE)) != 0

		if fGroupOwner || fGroupAdminAny {
			return sm.checkTelegramGroupAdminRights(msg, config)
		}
	}

	return false
}

func (sm *SecurityManager) checkTelegramGroupAdminRights(msg *Message, config int) bool {
	cacheKey := adminCacheKey(msg.ChatID, msg.SenderID, config)
	sm.mu.RLock()
	if entry, ok := sm.adminCache[cacheKey]; ok && entry.exp >= time.Now().Unix() {
		result := entry.result
		sm.mu.RUnlock()
		return result
	}
	sm.mu.RUnlock()

	peer, err := sm.client.ResolvePeer(msg.ChatID)
	if err != nil {
		return false
	}

	peerChan, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		// Standard group chat check
		res, err := sm.client.rawAPI.MessagesGetFullChat(sm.client.ctx, msg.ChatID)
		if err != nil {
			return false
		}
		var participant tg.ChatParticipantClass
		if fc, ok := res.FullChat.(*tg.ChatFull); ok {
			if cp, ok := fc.Participants.AsNotForbidden(); ok {
				for _, p := range cp.Participants {
					var uID int64
					switch pt := p.(type) {
					case *tg.ChatParticipant:
						uID = pt.UserID
					case *tg.ChatParticipantAdmin:
						uID = pt.UserID
					case *tg.ChatParticipantCreator:
						uID = pt.UserID
					}
					if uID == msg.SenderID {
						participant = p
						break
					}
				}
			}
		}

		// Decisions below are cached like the channel path is. Without this the
		// cache is read but never written for basic groups, so every command in
		// one costs a full MessagesGetFullChat plus a participant scan.
		if participant == nil {
			sm.setAdminCache(cacheKey, false)
			return false
		}

		switch participant.(type) {
		case *tg.ChatParticipantCreator:
			sm.setAdminCache(cacheKey, true)
			return true
		case *tg.ChatParticipantAdmin:
			if sm.anyAdmin || (config&GROUP_ADMIN) != 0 || (config&(GROUP_ADMIN_ADD_ADMINS|GROUP_ADMIN_CHANGE_INFO|GROUP_ADMIN_BAN_USERS|GROUP_ADMIN_DEL_MSGS|GROUP_ADMIN_PIN_MSGS|GROUP_ADMIN_INVITE)) != 0 {
				sm.setAdminCache(cacheKey, true)
				return true
			}
		}
		sm.setAdminCache(cacheKey, false)
		return false
	}

	inputChannel := &tg.InputChannel{
		ChannelID:  peerChan.ChannelID,
		AccessHash: peerChan.AccessHash,
	}

	inputUser, err := sm.client.ResolvePeer(msg.SenderID)
	if err != nil {
		return false
	}

	res, err := sm.client.rawAPI.ChannelsGetParticipant(sm.client.ctx, &tg.ChannelsGetParticipantRequest{
		Channel:     inputChannel,
		Participant: inputUser,
	})
	if err != nil {
		return false
	}

	switch pt := res.Participant.(type) {
	case *tg.ChannelParticipantCreator:
		sm.setAdminCache(cacheKey, true)
		return true
	case *tg.ChannelParticipantAdmin:
		if sm.anyAdmin {
			sm.setAdminCache(cacheKey, true)
			return true
		}
		if (config & GROUP_ADMIN) != 0 {
			sm.setAdminCache(cacheKey, true)
			return true
		}
		rights := pt.AdminRights
		if (config&GROUP_ADMIN_ADD_ADMINS) != 0 && rights.AddAdmins {
			sm.setAdminCache(cacheKey, true)
			return true
		}
		if (config&GROUP_ADMIN_CHANGE_INFO) != 0 && rights.ChangeInfo {
			sm.setAdminCache(cacheKey, true)
			return true
		}
		if (config&GROUP_ADMIN_BAN_USERS) != 0 && rights.BanUsers {
			sm.setAdminCache(cacheKey, true)
			return true
		}
		if (config&GROUP_ADMIN_DEL_MSGS) != 0 && rights.DeleteMessages {
			sm.setAdminCache(cacheKey, true)
			return true
		}
		if (config&GROUP_ADMIN_PIN_MSGS) != 0 && rights.PinMessages {
			sm.setAdminCache(cacheKey, true)
			return true
		}
		if (config&GROUP_ADMIN_INVITE) != 0 && rights.InviteUsers {
			sm.setAdminCache(cacheKey, true)
			return true
		}
	}

	sm.setAdminCache(cacheKey, false)
	return false
}

// adminCacheKey identifies a cached admin-rights decision. The permission mask
// is part of the key because the cached value answers "does this user satisfy
// *this* mask", not "is this user an admin". Keyed on chatID/userID alone (as
// the Python original was), a `true` cached for a command needing only
// GROUP_ADMIN_PIN_MSGS was replayed for one needing GROUP_ADMIN_BAN_USERS,
// granting rights the user does not hold; a cached `false` symmetrically denied
// commands they do.
func adminCacheKey(chatID, senderID int64, config int) string {
	return fmt.Sprintf("%d/%d/%d", chatID, senderID, config)
}

// adminCacheTTL and adminCacheMaxEntries bound the admin-rights cache. Entries
// carry an expiry but nothing reads them again once the chat goes quiet, so the
// map is swept on write; without that it grows for the process lifetime, one
// entry per distinct chat/user pair ever seen.
const (
	adminCacheTTL        = 300 * time.Second
	adminCacheMaxEntries = 4096
)

// setAdminCache stores a result with adminCacheTTL and keeps the cache bounded.
func (sm *SecurityManager) setAdminCache(key string, result bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	now := time.Now().Unix()
	if len(sm.adminCache) >= adminCacheMaxEntries {
		sm.sweepAdminCacheLocked(now)
		// Still saturated after dropping expired entries: reset rather than grow
		// without bound. Losing warm entries only costs a re-fetch.
		if len(sm.adminCache) >= adminCacheMaxEntries {
			sm.adminCache = make(map[string]adminCacheEntry, adminCacheMaxEntries)
		}
	}
	sm.adminCache[key] = adminCacheEntry{result: result, exp: now + int64(adminCacheTTL.Seconds())}
}

// sweepAdminCacheLocked drops expired entries. sm.mu must be held for writing.
func (sm *SecurityManager) sweepAdminCacheLocked(now int64) {
	for key, entry := range sm.adminCache {
		if entry.exp < now {
			delete(sm.adminCache, key)
		}
	}
}

func (sm *SecurityManager) getFlagsForCommand(command string) int {
	var reg *commandRegistration
	if sm.client.Loader != nil {
		reg, _ = sm.client.Loader.resolveCommand(command)
	}
	return sm.getFlagsForRegistration(command, reg)
}

func (sm *SecurityManager) getFlagsForRegistration(command string, reg *commandRegistration) int {
	boundingMask := sm.getBoundingMask()
	if reg != nil {
		for _, key := range []string{
			fmt.Sprintf("%s.%s", reg.OwnerName, reg.Name),
			fmt.Sprintf("%s.%s", reg.ownerKey, reg.Name),
		} {
			if mask, ok := sm.getMaskOverride(key); ok {
				return mask & boundingMask
			}
		}
	}
	if mask, ok := sm.getMaskOverride(command); ok {
		// Compatibility: an unqualified legacy mask is bound on first use to
		// its current owner. Unresolved commands still use bare masks, but a
		// later owner must have an explicit owner-qualified policy.
		if reg == nil || sm.bareMaskApplies(command, reg.ownerKey) {
			return mask & boundingMask
		}
	}

	if reg == nil {
		return sm.defaultMask & boundingMask
	}
	if reg.hasPermission {
		return reg.Permission & boundingMask
	}
	return sm.defaultMask & boundingMask
}

func (sm *SecurityManager) bareMaskApplies(command, ownerKey string) bool {
	command = strings.ToLower(command)
	ownerKey = strings.ToLower(ownerKey)
	sm.mu.Lock()
	defer sm.mu.Unlock()
	owners := sm.db.GetStringMap("goroku.security", "mask_owners", nil)
	if owner, ok := owners[command]; ok {
		return strings.EqualFold(owner, ownerKey)
	}
	if owners == nil {
		owners = make(map[string]string)
	}
	owners[command] = ownerKey
	if err := sm.db.SetStringMap("goroku.security", "mask_owners", owners); err != nil {
		L().Error("Failed to bind legacy security mask owner", zap.String("command", command), zap.Error(err))
		return false
	}
	return true
}

func (sm *SecurityManager) getBoundingMask() int {
	return sm.db.GetInt("goroku.security", "bounding_mask", DEFAULT_PERMISSIONS)
}

func (sm *SecurityManager) getMaskOverride(key string) (int, bool) {
	for _, owner := range []string{"goroku.security", "goroku/goroku/security"} {
		masks := sm.db.GetStringMap(owner, "masks", nil)
		for _, lookup := range []string{key, strings.ToLower(key)} {
			if val, exists := masks[lookup]; exists && val != "" {
				return intFromInterface(val, sm.defaultMask), true
			}
		}
	}
	return 0, false
}

func intFromInterface(v any, fallback int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		if parsed, err := strconv.Atoi(x); err == nil {
			return parsed
		}
	}
	return fallback
}

func (sm *SecurityManager) isCommandInModule(command, moduleName string) bool {
	if sm.client.Loader == nil {
		return false
	}
	reg, ok := sm.client.Loader.resolveCommand(command)
	return ok && registrationInModule(reg, moduleName)
}

func registrationInModule(reg *commandRegistration, moduleName string) bool {
	return reg != nil && strings.EqualFold(reg.OwnerName, moduleName)
}

func (sm *SecurityManager) AddRule(targetType string, targetID int64, ruleType, ruleName string, duration int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var expires int64
	if duration > 0 {
		expires = time.Now().Unix() + int64(duration)
	}

	newRule := SecurityRule{
		Target:     targetID,
		RuleType:   ruleType,
		Rule:       ruleName,
		Expires:    expires,
		EntityName: strconv.FormatInt(targetID, 10),
		EntityURL:  "",
	}

	return sm.addSecurityRuleLocked(targetType, newRule)
}

func (sm *SecurityManager) AddSecurityRule(targetType string, rule SecurityRule) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.addSecurityRuleLocked(targetType, rule)
}

func (sm *SecurityManager) addSecurityRuleLocked(targetType string, rule SecurityRule) error {
	userRules := sm.tsecUser.ToSlice()
	chatRules := sm.tsecChat.ToSlice()
	if targetType == "user" {
		userRules = append(userRules, rule)
	} else if targetType == "chat" {
		chatRules = append(chatRules, rule)
	} else {
		return fmt.Errorf("invalid security target type %q", targetType)
	}
	return sm.reloadRightsLocked(sm.owner.ToSlice(), userRules, chatRules, sm.sgroups, false)
}

// RemoveRules removes all security rules for a given target ID.
func (sm *SecurityManager) RemoveRules(targetType string, targetID int64) (bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	userRules := sm.tsecUser.ToSlice()
	chatRules := sm.tsecChat.ToSlice()
	var list []SecurityRule
	if targetType == "user" {
		list = userRules
	} else if targetType == "chat" {
		list = chatRules
	} else {
		return false, nil
	}

	found := false
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Target == targetID {
			list = append(list[:i], list[i+1:]...)
			found = true
		}
	}
	if found {
		if targetType == "user" {
			userRules = list
		} else {
			chatRules = list
		}
		if err := sm.reloadRightsLocked(sm.owner.ToSlice(), userRules, chatRules, sm.sgroups, false); err != nil {
			return false, err
		}
	}
	return found, nil
}

// RemoveRule removes a specific security rule for a given target ID and rule name.
func (sm *SecurityManager) RemoveRule(targetType string, targetID int64, ruleName string) (bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	userRules := sm.tsecUser.ToSlice()
	chatRules := sm.tsecChat.ToSlice()
	var list []SecurityRule
	if targetType == "user" {
		list = userRules
	} else if targetType == "chat" {
		list = chatRules
	} else {
		return false, nil
	}

	found := false
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Target == targetID && list[i].Rule == ruleName {
			list = append(list[:i], list[i+1:]...)
			found = true
		}
	}
	if found {
		if targetType == "user" {
			userRules = list
		} else {
			chatRules = list
		}
		if err := sm.reloadRightsLocked(sm.owner.ToSlice(), userRules, chatRules, sm.sgroups, false); err != nil {
			return false, err
		}
	}
	return found, nil
}

func (sm *SecurityManager) GetUserRules() []SecurityRule {
	return sm.tsecUser.ToSlice()
}

func (sm *SecurityManager) GetChatRules() []SecurityRule {
	return sm.tsecChat.ToSlice()
}

func (sm *SecurityManager) CheckTsec(userID int64, command string) bool {
	var reg *commandRegistration
	if sm.client.Loader != nil {
		reg, _ = sm.client.Loader.resolveCommand(command)
	}
	return sm.checkTsec(userID, command, reg)
}

func (sm *SecurityManager) checkTsecRegistration(userID int64, reg *commandRegistration) bool {
	return sm.checkTsec(userID, reg.Name, reg)
}

func (sm *SecurityManager) checkTsec(userID int64, command string, reg *commandRegistration) bool {
	// Computed before taking sm.mu: getFlagsForRegistration may take the write
	// lock to bind a legacy bare mask to its owner.
	if !delegationAllowed(sm.getFlagsForRegistration(command, reg)) {
		return false
	}

	sm.mu.RLock()
	for _, sgroup := range sm.sgroups {
		hasUser := false
		for _, u := range sgroup.Users {
			if u == userID {
				hasUser = true
				break
			}
		}
		if hasUser {
			for _, perm := range sgroup.Permissions {
				ruleType, _ := perm["rule_type"].(string)
				ruleName, _ := perm["rule"].(string)
				if ruleType == "command" && ruleName == command {
					sm.mu.RUnlock()
					return true
				}
				if ruleType == "module" && moduleRuleGrants(reg, ruleName) {
					sm.mu.RUnlock()
					return true
				}
			}
		}
	}
	sm.mu.RUnlock()

	for _, rule := range sm.GetUserRules() {
		if rule.Target == userID {
			if rule.RuleType == "command" && rule.Rule == command {
				return true
			}
			if rule.RuleType == "module" && moduleRuleGrants(reg, rule.Rule) {
				return true
			}
		}
	}

	return false
}

func (sm *SecurityManager) CheckTsecInline(userID int64, command string) bool {
	for _, rule := range sm.GetUserRules() {
		if rule.Target == userID && rule.RuleType == "inline" && rule.Rule == command {
			return true
		}
	}
	return false
}

func (sm *SecurityManager) IsOwner(userID int64) bool {
	if sm == nil || sm.client == nil || userID == 0 {
		return false
	}
	if userID == sm.client.TGIDValue() || sm.client.Me() != nil && userID == sm.client.Me().ID {
		return true
	}
	for _, id := range sm.owner.ToSlice() {
		if userID == id {
			return true
		}
	}
	return false
}

// IsAccountOwner checks identity only. It intentionally ignores outgoing
// status, command masks, sudo, temporary rules, and EVERYONE permissions.
func (sm *SecurityManager) IsAccountOwner(msg *Message) bool {
	return msg != nil && sm.IsOwner(msg.SenderID)
}

func (sm *SecurityManager) AddOwner(userID int64) (bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	owners := sm.owner.ToSlice()
	for _, id := range owners {
		if id == userID {
			return false, nil
		}
	}
	owners = append(owners, userID)
	if err := sm.reloadRightsLocked(owners, sm.tsecUser.ToSlice(), sm.tsecChat.ToSlice(), sm.sgroups, false); err != nil {
		return false, err
	}
	return true, nil
}

func (sm *SecurityManager) RemoveOwner(userID int64) (bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	owners := sm.owner.ToSlice()
	for i, id := range owners {
		if id != userID {
			continue
		}
		owners = append(owners[:i], owners[i+1:]...)
		if err := sm.reloadRightsLocked(owners, sm.tsecUser.ToSlice(), sm.tsecChat.ToSlice(), sm.sgroups, false); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (sm *SecurityManager) GetOwnerList() *PointerList[int64] {
	return sm.owner
}

func (sm *SecurityManager) IsUserInAllUsers(userID int64) bool {
	if sm.IsOwner(userID) {
		return true
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	for _, id := range sm.allUsers.ToSlice() {
		if id == userID {
			return true
		}
	}
	return false
}

func (sm *SecurityManager) CheckModuleAccess(userID int64, moduleName string) bool {
	if sm.IsOwner(userID) {
		return true
	}

	// Read blacklist user IDs
	for _, bid := range sm.db.GetInt64Slice("goroku.main", "blacklist_users", nil) {
		if userID == bid {
			return false
		}
	}

	// Check temporary tsec user rules
	for _, rule := range sm.GetUserRules() {
		if rule.Target == userID {
			if rule.RuleType == "module" && strings.EqualFold(rule.Rule, moduleName) {
				return true
			}
		}
	}

	// Check security groups (sgroups)
	sm.mu.RLock()
	for _, sgroup := range sm.sgroups {
		hasUser := false
		for _, u := range sgroup.Users {
			if u == userID {
				hasUser = true
				break
			}
		}
		if hasUser {
			for _, perm := range sgroup.Permissions {
				ruleType, _ := perm["rule_type"].(string)
				ruleName, _ := perm["rule"].(string)
				if ruleType == "module" && strings.EqualFold(ruleName, moduleName) {
					sm.mu.RUnlock()
					return true
				}
			}
		}
	}
	sm.mu.RUnlock()

	return false
}
