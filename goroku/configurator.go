package goroku

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TTYPrint(text string, tty bool) {
	if tty {
		fmt.Println(text)
	} else {
		fmt.Println(ansiRegex.ReplaceAllString(text, ""))
	}
}

func TTYInput(text string, tty bool) string {
	if tty {
		fmt.Print(text)
	} else {
		fmt.Print(ansiRegex.ReplaceAllString(text, ""))
	}
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input)
}

func APIConfig(ttyPtr *bool) {
	var tty bool
	if ttyPtr == nil {
		fmt.Println("\x1b[0;91mThe quick brown fox jumps over the lazy dog\x1b[0m")
		ans := TTYInput("Is the text above colored? [y/N]: ", true)
		tty = strings.ToLower(ans) == "y"
	} else {
		tty = *ttyPtr
	}

	if tty {
		PrintBanner("banner.txt")
	}

	TTYPrint("\x1b[0;95mWelcome to Goroku Userbot!\x1b[0m", tty)
	TTYPrint("\x1b[0;96m1. Go to https://my.telegram.org and login\x1b[0m", tty)
	TTYPrint("\x1b[0;96m2. Click on \x1b[1;96mAPI development tools\x1b[0m", tty)
	TTYPrint("\x1b[0;96m3. Create a new application, by entering the required details\x1b[0m", tty)
	TTYPrint("\x1b[0;96m4. Copy your \x1b[1;96mAPI ID\x1b[0;96m and \x1b[1;96mAPI hash\x1b[0m", tty)

	var apiIDStr string
	for {
		apiIDStr = TTYInput("\x1b[0;95mEnter API ID: \x1b[0m", tty)
		if apiIDStr == "" {
			TTYPrint("\x1b[0;91mCancelled\x1b[0m", tty)
			os.Exit(0)
		}
		_, err := strconv.ParseInt(apiIDStr, 10, 64)
		if err == nil {
			break
		}
		TTYPrint("\x1b[0;91mInvalid ID\x1b[0m", tty)
	}

	var apiHash string
	for {
		apiHash = TTYInput("\x1b[0;95mEnter API hash: \x1b[0m", tty)
		if apiHash == "" {
			TTYPrint("\x1b[0;91mCancelled\x1b[0m", tty)
			os.Exit(0)
		}
		apiHash = strings.TrimSpace(apiHash)
		// API Hash is 32 character hex
		isHex := len(apiHash) == 32
		if isHex {
			for _, r := range apiHash {
				if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
					isHex = false
					break
				}
			}
		}
		if isHex {
			break
		}
		TTYPrint("\x1b[0;91mInvalid hash\x1b[0m", tty)
	}

	apiID, _ := strconv.ParseInt(apiIDStr, 10, 64)
	SaveConfigKey("api_id", apiID)
	SaveConfigKey("api_hash", apiHash)
	TTYPrint("\x1b[0;92mAPI config saved\x1b[0m", tty)
}
