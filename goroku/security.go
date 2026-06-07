package goroku

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
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
	ALL                 = (1 << 13) - 1
)

type SecuredModule interface {
	CommandPermissions() map[string]int
}

type SecurityGroup struct {
	Name        string                   `json:"name"`
	Users       []int64                  `json:"users"`
	Permissions []map[string]interface{} `json:"permissions"`
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
	mu          sync.RWMutex
	client      *CustomTelegramClient
	db          *Database
	anyAdmin    bool
	defaultMask int
	tsecChat    *PointerList
	tsecUser    *PointerList
	owner       *PointerList
	allUsers    *PointerList
	sgroups     map[string]SecurityGroup
	// adminCache caches per-chat/per-user admin rights lookups (5-min TTL, mirrors Python security.py)
	adminCache map[string]adminCacheEntry
}

type adminCacheEntry struct {
	result bool
	exp    int64
}

func NewSecurityManager(client *CustomTelegramClient, db *Database) *SecurityManager {
	anyAdmin := false
	if val, ok := db.Get("goroku.security", "any_admin", false).(bool); ok {
		anyAdmin = val
	}

	defaultMask := OWNER
	if val, ok := db.Get("goroku.security", "default", OWNER).(float64); ok {
		defaultMask = int(val)
	}

	sm := &SecurityManager{
		client:      client,
		db:          db,
		anyAdmin:    anyAdmin,
		defaultMask: defaultMask,
		tsecChat:    NewPointerList(db, "goroku.security", "tsec_chat", []interface{}{}),
		tsecUser:    NewPointerList(db, "goroku.security", "tsec_user", []interface{}{}),
		owner:       NewPointerList(db, "goroku.security", "owner", []interface{}{}),
		allUsers:    NewPointerList(db, "goroku.security", "all_users", []interface{}{}),
		sgroups:     make(map[string]SecurityGroup),
		adminCache:  make(map[string]adminCacheEntry),
	}

	sm.reloadRights()
	return sm
}

func (sm *SecurityManager) reloadRights() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Ensure client owner ID is in the list of owners
	hasOwner := false
	ownerSlice := sm.owner.ToSlice()
	for _, idVal := range ownerSlice {
		var id int64
		switch v := idVal.(type) {
		case float64:
			id = int64(v)
		case int64:
			id = v
		}
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
	userRules := sm.getUserRules()
	for i := len(userRules) - 1; i >= 0; i-- {
		if userRules[i].Expires > 0 && userRules[i].Expires < now {
			sm.tsecUser.Remove(i)
		}
	}

	chatRules := sm.getChatRules()
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
	for _, rule := range sm.getUserRules() {
		tsecUsers = append(tsecUsers, rule.Target)
	}
	ownerSliceRaw := sm.owner.ToSlice()
	var ownerUsers []int64
	for _, idVal := range ownerSliceRaw {
		switch v := idVal.(type) {
		case float64:
			ownerUsers = append(ownerUsers, int64(v))
		case int64:
			ownerUsers = append(ownerUsers, v)
		}
	}

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
	var allUsersList []interface{}
	for id := range allUsersSet {
		allUsersList = append(allUsersList, id)
	}
	sm.allUsers.Clear()
	for _, id := range allUsersList {
		sm.allUsers.Append(id)
	}

	// Cleanup command_prefixes for users no longer in all_users (mirrors Python security.py:209-215)
	prefixesRaw := sm.db.Get("goroku.main", "command_prefixes", map[string]interface{}{})
	if prefixMap, ok := prefixesRaw.(map[string]interface{}); ok {
		for idStr := range prefixMap {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				continue
			}
			if _, ok := allUsersSet[id]; !ok {
				delete(prefixMap, idStr)
			}
		}
		sm.db.Set("goroku.main", "command_prefixes", prefixesRaw)
	}
}

func (sm *SecurityManager) ApplySgroups(sgroups map[string]SecurityGroup) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sgroups = sgroups
}

func (sm *SecurityManager) Check(msg *Message, command string) bool {
	sm.reloadRights()
	log.Printf("[Security] Check: SenderID=%d, Out=%t, client.TGID=%d, command=%s\n", msg.SenderID, msg.Out, sm.client.TGID, command)
	// First, if owner/client, bypass security check
	if msg.SenderID == sm.client.TGID || msg.Out {
		return true
	}

	// Read whitelist owner IDs
	for _, ownerVal := range sm.owner.ToSlice() {
		var id int64
		switch v := ownerVal.(type) {
		case float64:
			id = int64(v)
		case int64:
			id = v
		}
		if msg.SenderID == id {
			return true
		}
	}

	// Read blacklist user IDs
	blacklistVal := sm.db.Get("goroku.main", "blacklist_users", []interface{}{})
	if blacklistSlice, ok := blacklistVal.([]interface{}); ok {
		for _, bVal := range blacklistSlice {
			var bid int64
			switch v := bVal.(type) {
			case float64:
				bid = int64(v)
			case int64:
				bid = v
			}
			if msg.SenderID == bid {
				return false
			}
		}
	}

	// Get mask config for the command
	config := sm.getFlagsForCommand(command)

	// If everyone can access
	if (config & EVERYONE) != 0 {
		return true
	}

	// Check temporary tsec user rules
	for _, rule := range sm.getUserRules() {
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
	for _, rule := range sm.getChatRules() {
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
	modules, ok := sm.client.Loader.(*Modules)
	if !ok {
		return sm.defaultMask & boundingMask
	}

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
	return intFromInterface(sm.db.Get("goroku.security", "bounding_mask", DEFAULT_PERMISSIONS), DEFAULT_PERMISSIONS)
}

func (sm *SecurityManager) getMaskOverride(key string) (int, bool) {
	for _, owner := range []string{"goroku.security", "goroku/goroku/security"} {
		raw := sm.db.Get(owner, "masks", map[string]interface{}{})
		masks, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		for _, lookup := range []string{key, strings.ToLower(key)} {
			if val, exists := masks[lookup]; exists {
				return intFromInterface(val, sm.defaultMask), true
			}
		}
	}
	return 0, false
}

func intFromInterface(v interface{}, fallback int) int {
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
	modules, ok := sm.client.Loader.(*Modules)
	if !ok {
		return false
	}

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
}

// RemoveRules removes all security rules for a given target ID.
func (sm *SecurityManager) RemoveRules(targetType string, targetID int64) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var list *PointerList
	if targetType == "user" {
		list = sm.tsecUser
	} else if targetType == "chat" {
		list = sm.tsecChat
	} else {
		return false
	}

	any := false
	slice := list.ToSlice()
	for i := len(slice) - 1; i >= 0; i-- {
		if rule, ok := toSecurityRule(slice[i]); ok && rule.Target == targetID {
			list.Remove(i)
			any = true
		}
	}
	return any
}

// RemoveRule removes a specific security rule for a given target ID and rule name.
func (sm *SecurityManager) RemoveRule(targetType string, targetID int64, ruleName string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var list *PointerList
	if targetType == "user" {
		list = sm.tsecUser
	} else if targetType == "chat" {
		list = sm.tsecChat
	} else {
		return false
	}

	any := false
	slice := list.ToSlice()
	for i := len(slice) - 1; i >= 0; i-- {
		if rule, ok := toSecurityRule(slice[i]); ok && rule.Target == targetID && rule.Rule == ruleName {
			list.Remove(i)
			any = true
		}
	}
	return any
}

func toSecurityRule(item interface{}) (SecurityRule, bool) {
	var rule SecurityRule
	if bytes, err := json.Marshal(item); err == nil {
		if err := json.Unmarshal(bytes, &rule); err == nil {
			return rule, true
		}
	}
	return SecurityRule{}, false
}

func (sm *SecurityManager) getUserRules() []SecurityRule {
	slice := sm.tsecUser.ToSlice()
	var res []SecurityRule
	for _, item := range slice {
		if bytes, err := json.Marshal(item); err == nil {
			var rule SecurityRule
			if err := json.Unmarshal(bytes, &rule); err == nil {
				res = append(res, rule)
			}
		}
	}
	return res
}

func (sm *SecurityManager) getChatRules() []SecurityRule {
	slice := sm.tsecChat.ToSlice()
	var res []SecurityRule
	for _, item := range slice {
		if bytes, err := json.Marshal(item); err == nil {
			var rule SecurityRule
			if err := json.Unmarshal(bytes, &rule); err == nil {
				res = append(res, rule)
			}
		}
	}
	return res
}
