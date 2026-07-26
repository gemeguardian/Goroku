package modules

import (
	"fmt"
	"strings"

	"goroku/goroku"
)

const (
	moduleDigestsOwner = "Loader"
	moduleDigestsKey   = "module_digests"
)

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

// verifyModuleContentDigest enforces exact source integrity for persisted
// modules. Fresh local runtime sources may be loaded without a prior digest;
// remote restore always requires one.
func verifyModuleContentDigest(db *goroku.Database, modName string, body []byte, requireDigest bool) error {
	digest := contentSHA256(body)
	if expected := moduleContentDigests(db)[modName]; expected != "" {
		if !strings.EqualFold(expected, digest) {
			return fmt.Errorf("module %s content digest mismatch (expected %s…, got %s…)", modName, shortDigest(expected), shortDigest(digest))
		}
		return nil
	}
	if requireDigest {
		return fmt.Errorf("module %s has no persisted content digest (sha256=%s…)", modName, shortDigest(digest))
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
