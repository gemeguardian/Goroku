package goroku

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"reflect"

	"goroku/goroku/inline"
	"goroku/goroku/utils"

	"github.com/gotd/td/tg"
)

func (h *Goroku) initClient(tgID int64, sessionPath string, customModules []Module) (*CustomTelegramClient, error) {
	utils.SecureFile(sessionPath)
	db := NewDatabase(tgID)
	redisURI := os.Getenv("REDIS_URL")
	if redisURI == "" {
		if val := GetConfigKey("redis_uri"); val != nil {
			redisURI = fmt.Sprintf("%v", val)
		}
	}
	db.Init(redisURI)

	client := NewCustomTelegramClient(tgID)
	client.APIID = h.APIID
	client.APIHash = h.APIHash
	client.SessionPath = sessionPath
	client.GorokuDB = db
	db.client = client

	if err := client.Connect(); err != nil {
		return nil, err
	}

	loader := NewModules(client, db)
	client.Loader = loader

	inlineMgr := inline.NewInlineManager(client, db, loader)
	client.GorokuInline = inlineMgr

	h.registerBuiltInModules(loader)
	for _, mod := range customModules {
		if err := loader.RegisterModule(cloneModule(mod)); err != nil {
			log.Printf("Failed to register module %s: %v\n", mod.Name(), err)
		}
	}

	disp := NewCommandDispatcher(loader, client, db)
	loader.SetDispatcher(disp)

	loader.SendReady()

	h.sendBadge(client, db)

	h.Clients = append(h.Clients, client)
	h.DBs = append(h.DBs, db)
	h.Loaders = append(h.Loaders, loader)

	return client, nil
}

func cloneModule(mod Module) Module {
	if mod == nil {
		return nil
	}
	t := reflect.TypeOf(mod)
	if t.Kind() == reflect.Ptr {
		return reflect.New(t.Elem()).Interface().(Module)
	}
	return reflect.New(t).Interface().(Module)
}

func (h *Goroku) sendBadge(client *CustomTelegramClient, db *Database) {
	me, err := client.GetMe()
	if err != nil {
		return
	}
	var name string
	if u, ok := me.(*tg.User); ok {
		if u.FirstName != "" {
			name = u.FirstName
		} else {
			name = u.Username
		}
	} else {
		name = client.Username
	}

	uptime := utils.FormattedUptime()
	platform := utils.GetPlatformName()
	emoji := utils.GetPlatformEmoji()

	msg := fmt.Sprintf(
		"🪐 <b>Goroku Userbot</b> started!\n\n"+
			"👤 <b>Account:</b> %s\n"+
			"🖥 <b>Platform:</b> %s %s\n"+
			"⏱ <b>Uptime:</b> %s\n"+
			"📦 <b>Version:</b> %s",
		name, platform, emoji, uptime, GetVersionString(),
	)

	_, _ = client.SendMessage(client.TGID, msg)
}

func (h *Goroku) registerBuiltInModules(loader *Modules) {
	// Built-in modules registration
}

func GenerateAppName() string {
	latin := []string{
		"Amor", "Arbor", "Astra", "Aurum", "Bellum", "Caelum",
		"Calor", "Candor", "Carpe", "Celer", "Certo", "Cibus",
		"Civis", "Clemens", "Coetus", "Cogito", "Conexus",
	}
	return fmt.Sprintf("%s %s %s", latin[rand.Intn(len(latin))], latin[rand.Intn(len(latin))], latin[rand.Intn(len(latin))])
}

func generateRandomSystemVersion() string {
	systems := []string{
		"Ubuntu 22.04", "Ubuntu 24.04", "Fedora 38",
		"Debian 12 Bookworm", "Arch Linux", "CentOS Stream 9",
		"openSUSE Leap 15.5", "Manjaro 23.0", "Pop!_OS 22.04",
		"Linux Mint 21.2", "Kali Linux 2023.3",
	}
	return systems[rand.Intn(len(systems))]
}
