package modules

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"goroku/goroku"
	"goroku/goroku/utils"
)

// Legacy name-based MD5 hashes remain secondary identifiers for session_allow /
// resolve lookups. Prefer content SHA-256 digests for trust/allow decisions.

const (
	trustedDigestsKey  = "trusted_digests"
	moduleDigestsOwner = "Loader"
	moduleDigestsKey   = "module_digests"
)

// parseInstallArgs strips confirm tokens from command payloads so they never
// poison URLs (dlmod) or source bodies (loadmod). Spacing/newlines in source
// bodies are preserved; only whole-token flags are removed.
func parseInstallArgs(raw string) (payload string, confirmed bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	allowBareConfirm := !strings.Contains(raw, "\n")
	var b strings.Builder
	b.Grow(len(raw))
	i := 0
	for i < len(raw) {
		// Copy runs of whitespace (including newlines) unchanged.
		if unicode.IsSpace(rune(raw[i])) {
			start := i
			for i < len(raw) && unicode.IsSpace(rune(raw[i])) {
				i++
			}
			b.WriteString(raw[start:i])
			continue
		}
		// Read next non-space token.
		start := i
		for i < len(raw) && !unicode.IsSpace(rune(raw[i])) {
			i++
		}
		token := raw[start:i]
		lower := strings.ToLower(token)
		switch lower {
		case "-confirm", "--confirm":
			confirmed = true
			continue
		case "confirm":
			if allowBareConfirm {
				confirmed = true
				continue
			}
		}
		b.WriteString(token)
	}
	return strings.TrimSpace(b.String()), confirmed
}

func hasInstallConfirmToken(msg *goroku.Message) bool {
	if msg == nil {
		return false
	}
	raw := utils.GetArgsRaw(msg.RawText)
	if raw == "" {
		raw = utils.GetArgsRaw(msg.Text)
	}
	_, confirmed := parseInstallArgs(raw)
	return confirmed
}

func pluginSecurityStringSlice(db *goroku.Database, key string) ([]string, error) {
	if db == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	raw, err := db.Get("GorokuPluginSecurity", key, []any{})
	if err != nil {
		return nil, err
	}
	switch slice := raw.(type) {
	case []string:
		return append([]string(nil), slice...), nil
	case []any:
		out := make([]string, 0, len(slice))
		for _, item := range slice {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("GorokuPluginSecurity %s contains type %T, want string", key, item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("GorokuPluginSecurity %s has type %T, want string slice", key, raw)
	}
}

func isContentDigestTrusted(db *goroku.Database, digest string) bool {
	if db == nil || digest == "" {
		return false
	}
	digests, err := pluginSecurityStringSlice(db, trustedDigestsKey)
	if err != nil {
		return false
	}
	for _, d := range digests {
		if strings.EqualFold(d, digest) {
			return true
		}
	}
	// session_allow may contain content digests (64-hex) in addition to legacy name MD5.
	sessionAllow, err := pluginSecurityStringSlice(db, "session_allow")
	if err != nil {
		return false
	}
	for _, d := range sessionAllow {
		if strings.EqualFold(d, digest) {
			return true
		}
	}
	return false
}

func trustContentDigest(db *goroku.Database, digest string) error {
	if db == nil || digest == "" {
		return nil
	}
	digests, err := pluginSecurityStringSlice(db, trustedDigestsKey)
	if err != nil {
		return err
	}
	if stringIndex(digests, digest, true) != -1 {
		return nil
	}
	digests = append(digests, digest)
	return db.SetStringSlice("GorokuPluginSecurity", trustedDigestsKey, digests)
}

func untrustContentDigest(db *goroku.Database, digest string) error {
	if db == nil || digest == "" {
		return nil
	}
	digests, err := pluginSecurityStringSlice(db, trustedDigestsKey)
	if err != nil {
		return err
	}
	idx := stringIndex(digests, digest, true)
	if idx == -1 {
		return nil
	}
	return db.SetStringSlice("GorokuPluginSecurity", trustedDigestsKey, removeStringAt(digests, idx))
}

// ensureUnsafeInstallAllowed requires trusted content digest or explicit owner
// confirmation before compiling/loading native plugin code.
// confirmed=true covers interactive UI actions (install button). msg==nil is
// used for non-interactive reloads of already-persisted modules.
func ensureUnsafeInstallAllowed(msg *goroku.Message, db *goroku.Database, body []byte, confirmed bool) error {
	if msg == nil {
		return nil
	}
	digest := contentSHA256(body)
	if isContentDigestTrusted(db, digest) {
		return nil
	}
	if confirmed || hasInstallConfirmToken(msg) {
		return nil
	}
	short := digest
	if len(short) > 16 {
		short = short[:16]
	}
	return fmt.Errorf("untrusted native module (sha256=%s…); review source and re-run with -confirm", short)
}

func trustInstalledModuleContent(db *goroku.Database, modName string) error {
	digest, err := installedModuleContentDigest(modName)
	if err != nil || digest == "" {
		return nil
	}
	return trustContentDigest(db, digest)
}

func untrustInstalledModuleContent(db *goroku.Database, modName string) error {
	digest, err := installedModuleContentDigest(modName)
	if err != nil || digest == "" {
		return nil
	}
	return untrustContentDigest(db, digest)
}

func installedModuleContentDigest(modName string) (string, error) {
	path, err := findInstalledModuleSource(modName)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	return contentSHA256(body), nil
}

func moduleContentDigests(db *goroku.Database) map[string]string {
	if db == nil {
		return map[string]string{}
	}
	return db.GetStringMap(moduleDigestsOwner, moduleDigestsKey, nil)
}

func setModuleContentDigest(db *goroku.Database, modName, digest string) error {
	if db == nil || modName == "" || digest == "" {
		return nil
	}
	digests := moduleContentDigests(db)
	digests[modName] = digest
	return db.SetStringMap(moduleDigestsOwner, moduleDigestsKey, digests)
}

func clearModuleContentDigest(db *goroku.Database, modName string) error {
	if db == nil || modName == "" {
		return nil
	}
	digests := moduleContentDigests(db)
	if _, ok := digests[modName]; !ok {
		return nil
	}
	delete(digests, modName)
	return db.SetStringMap(moduleDigestsOwner, moduleDigestsKey, digests)
}

// verifyPinnedOrTrustedContent refuses boot/restore of content that does not
// match the stored pin or an explicit content trust entry.
func verifyPinnedOrTrustedContent(db *goroku.Database, modName string, body []byte, requirePin bool) error {
	digest := contentSHA256(body)
	if expected := moduleContentDigests(db)[modName]; expected != "" {
		if !strings.EqualFold(expected, digest) {
			return fmt.Errorf("module %s content digest mismatch (pinned %s…, got %s…)", modName, shortDigest(expected), shortDigest(digest))
		}
		return nil
	}
	if isContentDigestTrusted(db, digest) {
		return nil
	}
	if requirePin {
		return fmt.Errorf("module %s has no pinned digest and content is not trusted (sha256=%s…)", modName, shortDigest(digest))
	}
	return nil
}

func shortDigest(digest string) string {
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}

func callbackIsAccountOwner(client *goroku.CustomTelegramClient, fromID int64) bool {
	if client == nil || fromID == 0 {
		return false
	}
	if fromID == client.TGID {
		return true
	}
	if sm := client.GetSecurityManager(); sm != nil {
		return sm.IsOwner(fromID)
	}
	return false
}

func requireOwnerCallback(client *goroku.CustomTelegramClient, call interface{ Answer(string, bool) error }, fromID int64) bool {
	if callbackIsAccountOwner(client, fromID) {
		return true
	}
	_ = call.Answer("Owner only", true)
	return false
}
