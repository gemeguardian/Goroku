package goroku

import (
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
	// adminCache caches per-chat/per-user admin rights lookups (5-min TTL, mirrors Python security.py)
	adminCache map[string]adminCacheEntry
}

type adminCacheEntry struct {
	result bool
	exp    int64
}

func NewSecurityManager(client *CustomTelegramClient, db *Database) *SecurityManager {
	anyAdmin := db.GetBool("goroku.security", "any_admin", false)
	defaultMask := db.GetInt("goroku.security", "default", OWNER)

	sm := &SecurityManager{
		client:               client,
		db:                   db,
		anyAdmin:             anyAdmin,
		defaultMask:          defaultMask,
		tsecChat:             NewPointerList[SecurityRule](db, "goroku.security", "tsec_chat", nil),
		tsecUser:             NewPointerList[SecurityRule](db, "goroku.security", "tsec_user", nil),
		owner:                NewPointerList[int64](db, "goroku.security", "owner", nil),
		allUsers:             NewPointerList[int64](db, "goroku.security", "all_users", nil),
		sgroups:              make(map[string]SecurityGroup),
		adminCache:           make(map[string]adminCacheEntry),
		rightsReloadInterval: time.Minute,
		stopCh:               make(chan struct{}),
	}

	sm.reloadRights()
	sm.startRightsReloader()
	return sm
}

func (sm *SecurityManager) startRightsReloader() {
	if sm.rightsReloadInterval <= 0 {
		return
	}

	ticker := time.NewTicker(sm.rightsReloadInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sm.reloadRights()
			case <-sm.stopCh:
				return
			}
		}
	}()
}

func (sm *SecurityManager) Stop() {
	select {
	case <-sm.stopCh:
	default:
		close(sm.stopCh)
	}
}

func (sm *SecurityManager) ReloadRights() {
	sm.reloadRights()
}

func (sm *SecurityManager) reloadRights() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.reloadRightsLocked()
}

func (sm *SecurityManager) reloadRightsLocked() {
	// Ensure client owner ID is in the list of owners
	hasOwner := false
	for _, id := range sm.owner.ToSlice() {
		if id == sm.client.TGID {
			hasOwner = true
			break
		}
	}
	if !hasOwner {
		sm.owner.Append(sm.client.TGID)
	}

	// Clean up expired rules
	now := time.Now().Unix()
	userRules := sm.GetUserRules()
	for i := len(userRules) - 1; i >= 0; i-- {
		if userRules[i].Expires > 0 && userRules[i].Expires < now {
			sm.tsecUser.Remove(i)
		}
	}

	chatRules := sm.GetChatRules()
	for i := len(chatRules) - 1; i >= 0; i-- {
		if chatRules[i].Expires > 0 && chatRules[i].Expires < now {
			sm.tsecChat.Remove(i)
		}
	}
	// Rebuild all_users list (mirrors Python _reload_rights)
	var sgroupUsers []int64
	for _, g := range sm.sgroups {
		sgroupUsers = append(sgroupUsers, g.Users...)
	}
	var tsecUsers []int64
	for _, rule := range sm.GetUserRules() {
		tsecUsers = append(tsecUsers, rule.Target)
	}
	ownerUsers := sm.owner.ToSlice()

	allUsersSet := make(map[int64]struct{})
	for _, id := range sgroupUsers {
		allUsersSet[id] = struct{}{}
	}
	for _, id := range tsecUsers {
		allUsersSet[id] = struct{}{}
	}
	for _, id := range ownerUsers {
		allUsersSet[id] = struct{}{}
	}
	var allUsersList []int64
	for id := range allUsersSet {
		allUsersList = append(allUsersList, id)
	}
	sm.allUsers.Clear()
	sm.allUsers.Extend(allUsersList)

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
	sm.db.SetStringMap("goroku.main", "command_prefixes", prefixes)
}

func (sm *SecurityManager) ApplySgroups(sgroups map[string]SecurityGroup) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sgroups = sgroups
	sm.reloadRightsLocked()
}

func (sm *SecurityManager) Check(msg *Message, command string) bool {
	L().Info("[Security] Check: SenderID={0}, Out={1}, client.TGID={2}, command={3}", zap.Any("arg0", msg.SenderID), zap.Any("arg1", msg.Out), zap.Any("arg2", sm.client.TGID), zap.Any("arg3", command))
	// First, if owner/client, bypass security check
	if msg.SenderID == sm.client.TGID || msg.Out {
		return true
	}

	// Read whitelist owner IDs
	for _, id := range sm.owner.ToSlice() {
		if msg.SenderID == id {
			return true
		}
	}

	// Read blacklist user IDs
	for _, bid := range sm.db.GetInt64Slice("goroku.main", "blacklist_users", nil) {
		if msg.SenderID == bid {
			return false
		}
	}

	// Get mask config for the command
	config := sm.getFlagsForCommand(command)

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

	// Check temporary tsec user rules
	for _, rule := range sm.GetUserRules() {
		if rule.Target == msg.SenderID {
			if rule.RuleType == "command" && rule.Rule == command {
				return true
			}
			// If rule is module-wide
			if rule.RuleType == "module" {
				if sm.isCommandInModule(command, rule.Rule) {
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
			if rule.RuleType == "module" && sm.isCommandInModule(command, rule.Rule) {
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
				if ruleType == "module" && sm.isCommandInModule(command, ruleName) {
					sm.mu.RUnlock()
					return true
				}
			}
		}
	}
	sm.mu.RUnlock()

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
	// Cache key: chatID/userID (mirrors Python's self._cache[f"{chat_id}/{user_id}"])
	cacheKey := fmt.Sprintf("%d/%d", msg.ChatID, msg.SenderID)
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

		if participant == nil {
			return false
		}

		switch participant.(type) {
		case *tg.ChatParticipantCreator:
			return true
		case *tg.ChatParticipantAdmin:
			if sm.anyAdmin || (config&GROUP_ADMIN) != 0 || (config&(GROUP_ADMIN_ADD_ADMINS|GROUP_ADMIN_CHANGE_INFO|GROUP_ADMIN_BAN_USERS|GROUP_ADMIN_DEL_MSGS|GROUP_ADMIN_PIN_MSGS|GROUP_ADMIN_INVITE)) != 0 {
				return true
			}
		}
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

// setAdminCache stores a result with 5-minute TTL.
func (sm *SecurityManager) setAdminCache(key string, result bool) {
	sm.mu.Lock()
	sm.adminCache[key] = adminCacheEntry{result: result, exp: time.Now().Unix() + 300}
	sm.mu.Unlock()
}

func (sm *SecurityManager) getFlagsForCommand(command string) int {
	boundingMask := sm.getBoundingMask()
	if mask, ok := sm.getMaskOverride(command); ok {
		return mask & boundingMask
	}

	if sm.client.Loader == nil {
		return sm.defaultMask & boundingMask
	}
	modules := sm.client.Loader

	for _, mod := range modules.GetModules() {
		if _, exists := mod.Commands()[command]; exists {
			for _, key := range []string{
				fmt.Sprintf("%s.%s", mod.Name(), command),
				fmt.Sprintf("%s.%s", strings.ToLower(mod.Name()), strings.ToLower(command)),
			} {
				if mask, ok := sm.getMaskOverride(key); ok {
					return mask & boundingMask
				}
			}
			if secMod, ok := mod.(SecuredModule); ok {
				if mask, ok := secMod.CommandPermissions()[command]; ok {
					return mask & boundingMask
				}
			}
			break
		}
	}
	return sm.defaultMask & boundingMask
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
	modules := sm.client.Loader

	for _, mod := range modules.GetModules() {
		if strings.EqualFold(mod.Name(), moduleName) {
			if _, exists := mod.Commands()[command]; exists {
				return true
			}
		}
	}
	return false
}

func (sm *SecurityManager) AddRule(targetType string, targetID int64, ruleType, ruleName string, duration int) {
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

	if targetType == "user" {
		sm.tsecUser.Append(newRule)
	} else if targetType == "chat" {
		sm.tsecChat.Append(newRule)
	}

	sm.reloadRightsLocked()
}

func (sm *SecurityManager) AddSecurityRule(targetType string, rule SecurityRule) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if targetType == "user" {
		sm.tsecUser.Append(rule)
	} else if targetType == "chat" {
		sm.tsecChat.Append(rule)
	}
	sm.reloadRightsLocked()
}

// RemoveRules removes all security rules for a given target ID.
func (sm *SecurityManager) RemoveRules(targetType string, targetID int64) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var list *PointerList[SecurityRule]
	if targetType == "user" {
		list = sm.tsecUser
	} else if targetType == "chat" {
		list = sm.tsecChat
	} else {
		return false
	}

	found := false
	slice := list.ToSlice()
	for i := len(slice) - 1; i >= 0; i-- {
		if slice[i].Target == targetID {
			list.Remove(i)
			found = true
		}
	}
	if found {
		sm.reloadRightsLocked()
	}
	return found
}

// RemoveRule removes a specific security rule for a given target ID and rule name.
func (sm *SecurityManager) RemoveRule(targetType string, targetID int64, ruleName string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var list *PointerList[SecurityRule]
	if targetType == "user" {
		list = sm.tsecUser
	} else if targetType == "chat" {
		list = sm.tsecChat
	} else {
		return false
	}

	found := false
	slice := list.ToSlice()
	for i := len(slice) - 1; i >= 0; i-- {
		if slice[i].Target == targetID && slice[i].Rule == ruleName {
			list.Remove(i)
			found = true
		}
	}
	if found {
		sm.reloadRightsLocked()
	}
	return found
}

func (sm *SecurityManager) GetUserRules() []SecurityRule {
	return sm.tsecUser.ToSlice()
}

func (sm *SecurityManager) GetChatRules() []SecurityRule {
	return sm.tsecChat.ToSlice()
}

func (sm *SecurityManager) CheckTsec(userID int64, command string) bool {
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
				if ruleType == "module" && sm.isCommandInModule(command, ruleName) {
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
			if rule.RuleType == "module" && sm.isCommandInModule(command, rule.Rule) {
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
	if userID == sm.client.TGID {
		return true
	}
	for _, id := range sm.owner.ToSlice() {
		if userID == id {
			return true
		}
	}
	return false
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
