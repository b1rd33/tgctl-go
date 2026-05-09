//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

type command struct {
	Name    string
	Short   string
	Use     string
	Flags   []flag
	Example string
}

type flag struct {
	Name        string
	Description string
}

func main() {
	rootHelp := mustHelp("./tg", "--help")
	commands := parseCommands(rootHelp)
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })

	fmt.Println("# Commands")
	fmt.Println()
	fmt.Println("`tg --help` shows all 70 commands. This page is generated from the local Cobra help output, then kept as plain Markdown for the docs site.")
	fmt.Println()
	fmt.Println("Every command supports the global flags shown by `tg --help`: `--account`, `--full`, `--json`, `--human`, `--lock-wait`, `--read-only`, and `--version` where applicable.")
	fmt.Println()
	fmt.Println("## Index")
	fmt.Println()
	fmt.Println("| Command | What |")
	fmt.Println("|---|---|")

	for i := range commands {
		help := mustHelp("./tg", commands[i].Name, "--help")
		commands[i].Use = parseUse(help)
		if short := parseShort(help); short != "" {
			commands[i].Short = short
		}
		commands[i].Flags = parseFlags(help)
		commands[i].Example = exampleFor(commands[i].Name, commands[i].Use)
		fmt.Printf("| [`tg %s`](#tg-%s) | %s |\n", commands[i].Name, anchor(commands[i].Name), commands[i].Short)
	}

	fmt.Println()
	for _, cmd := range commands {
		fmt.Printf("## `tg %s`\n\n", cmd.Name)
		fmt.Println(cmd.Short)
		fmt.Println()
		if cmd.Use != "" {
			fmt.Println("**Use**")
			fmt.Println()
			fmt.Println("```text")
			fmt.Println(cmd.Use)
			fmt.Println("```")
			fmt.Println()
		}
		fmt.Println("**Example**")
		fmt.Println()
		fmt.Println("```bash")
		fmt.Println(cmd.Example)
		fmt.Println("```")
		fmt.Println()
		if len(cmd.Flags) > 0 {
			fmt.Println("**Flags**")
			fmt.Println()
			fmt.Println("| Flag | Description |")
			fmt.Println("|---|---|")
			for _, f := range cmd.Flags {
				fmt.Printf("| `%s` | %s |\n", escapePipes(f.Name), escapePipes(f.Description))
			}
			fmt.Println()
		}
	}
}

func mustHelp(args ...string) string {
	cmd := exec.Command(args[0], args[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("%s failed: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}

func parseCommands(help string) []command {
	var commands []command
	inCommands := false
	re := regexp.MustCompile(`^\s{2}([a-z][a-z0-9-]*)\s{2,}(.+)$`)
	for _, line := range strings.Split(help, "\n") {
		switch {
		case strings.TrimSpace(line) == "Available Commands:":
			inCommands = true
			continue
		case inCommands && strings.TrimSpace(line) == "Flags:":
			inCommands = false
		}
		if !inCommands {
			continue
		}
		match := re.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		commands = append(commands, command{Name: match[1], Short: strings.TrimSpace(match[2])})
	}
	return commands
}

func parseUse(help string) string {
	lines := strings.Split(help, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "Usage:" {
			continue
		}
		var use []string
		for _, candidate := range lines[i+1:] {
			if strings.TrimSpace(candidate) == "" {
				break
			}
			use = append(use, strings.TrimSpace(candidate))
		}
		return strings.Join(use, "\n")
	}
	return ""
}

func parseShort(help string) string {
	for _, line := range strings.Split(help, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Usage:") {
			continue
		}
		if strings.HasPrefix(line, "Aliases:") || strings.HasPrefix(line, "Examples:") {
			return ""
		}
		return line
	}
	return ""
}

func parseFlags(help string) []flag {
	lines := strings.Split(help, "\n")
	re := regexp.MustCompile(`^\s{2,}((?:-[A-Za-z],\s*)?--[A-Za-z0-9-]+(?:\s+[A-Za-z0-9_.<>-]+)?)(?:\s{2,}|\t+)(.+)$`)
	var flags []flag
	for i, line := range lines {
		if strings.TrimSpace(line) != "Flags:" {
			continue
		}
		for _, candidate := range lines[i+1:] {
			if strings.TrimSpace(candidate) == "" {
				break
			}
			match := re.FindStringSubmatch(candidate)
			if match == nil {
				continue
			}
			flags = append(flags, flag{
				Name:        strings.TrimSpace(match[1]),
				Description: strings.TrimSpace(match[2]),
			})
		}
	}
	return flags
}

func exampleFor(name, use string) string {
	switch name {
	case "login":
		return "tg login"
	case "me":
		return "tg me --json"
	case "doctor":
		return "tg doctor --json"
	case "version":
		return "tg version --json"
	case "discover":
		return "tg discover --json"
	case "sync-contacts":
		return "tg sync-contacts --allow-write --json"
	case "backfill-entities":
		return "tg backfill-entities --json"
	case "backfill":
		return "tg backfill 1240314255 --max-messages 100 --allow-write --json"
	case "show":
		return "tg show 1240314255 --limit 5 --json"
	case "search":
		return "tg search 1240314255 \"shipping\" --limit 20 --json"
	case "list-msgs":
		return "tg list-msgs 1240314255 --limit 10 --json"
	case "get-msg":
		return "tg get-msg 1240314255 1 --json"
	case "send":
		return "tg send 1240314255 \"hello\" --allow-write --json"
	case "send-by-username":
		return "tg send-by-username @username \"hello\" --allow-write --json"
	case "edit-msg":
		return "tg edit-msg 1240314255 1 \"updated\" --allow-write --json"
	case "delete-msg":
		return "tg delete-msg 1240314255 1 --allow-write --confirm 1240314255 --json"
	case "forward":
		return "tg forward 1240314255 1240314255 1 --allow-write --json"
	case "pin-msg":
		return "tg pin-msg 1240314255 1 --allow-write --json"
	case "unpin-msg":
		return "tg unpin-msg 1240314255 1 --allow-write --json"
	case "mark-read":
		return "tg mark-read 1240314255 --up-to 1 --allow-write --json"
	case "react":
		return "tg react 1240314255 1 \"👍\" --allow-write --json"
	case "upload-photo":
		return "tg upload-photo 1240314255 ./photo.png --allow-write --json"
	case "upload-document":
		return "tg upload-document 1240314255 ./file.txt --allow-write --json"
	case "upload-video":
		return "tg upload-video 1240314255 ./video.mp4 --allow-write --json"
	case "upload-voice":
		return "tg upload-voice 1240314255 ./voice.ogg --allow-write --json"
	case "topics-list":
		return "tg topics-list <forum-chat-id> --json"
	case "topic-create":
		return "tg topic-create <forum-chat-id> \"Support\" --allow-write --json"
	case "topic-edit":
		return "tg topic-edit <forum-chat-id> 1 --title \"Renamed\" --allow-write --json"
	case "topic-pin":
		return "tg topic-pin <forum-chat-id> 1 --allow-write --json"
	case "topic-unpin":
		return "tg topic-unpin <forum-chat-id> 1 --allow-write --json"
	case "folders-list":
		return "tg folders-list --json"
	case "folder-show":
		return "tg folder-show 2 --json"
	case "folder-create":
		return "tg folder-create \"support\" --include-chats 1240314255 --allow-write --json"
	case "folder-edit":
		return "tg folder-edit 2 --name \"support\" --allow-write --json"
	case "folder-delete":
		return "tg folder-delete 2 --allow-write --confirm 2 --json"
	case "folder-add-chat":
		return "tg folder-add-chat 2 1240314255 --allow-write --json"
	case "folder-remove-chat":
		return "tg folder-remove-chat 2 1240314255 --allow-write --json"
	case "folders-reorder":
		return "tg folders-reorder 2,3,4 --allow-write --json"
	case "chats-info":
		return "tg chats-info 1240314255 --json"
	case "chat-members":
		return "tg chat-members <group-chat-id> --limit 50 --json"
	case "chat-title":
		return "tg chat-title <group-chat-id> \"New title\" --allow-write --json"
	case "chat-photo":
		return "tg chat-photo <group-chat-id> ./photo.png --allow-write --json"
	case "chat-description":
		return "tg chat-description <group-chat-id> \"Description\" --allow-write --json"
	case "chat-invite-link":
		return "tg chat-invite-link <group-chat-id> --allow-write --json"
	case "chat-pinned-list":
		return "tg chat-pinned-list 1240314255 --json"
	case "set-permissions":
		return "tg set-permissions <group-chat-id> --send-messages --allow-write --json"
	case "promote":
		return "tg promote <group-chat-id> 1240314255 --allow-write --confirm 1240314255 --json"
	case "demote":
		return "tg demote <group-chat-id> 1240314255 --allow-write --confirm 1240314255 --json"
	case "ban-from-chat":
		return "tg ban-from-chat <group-chat-id> 1240314255 --allow-write --confirm 1240314255 --json"
	case "unban-from-chat":
		return "tg unban-from-chat <group-chat-id> 1240314255 --allow-write --confirm 1240314255 --json"
	case "kick":
		return "tg kick <group-chat-id> 1240314255 --allow-write --confirm 1240314255 --json"
	case "listen":
		return "tg listen --once --json"
	case "account-sessions":
		return "tg account-sessions --json"
	case "terminate-session":
		return "tg terminate-session <session-hash> --allow-write --confirm <session-hash> --json"
	case "block-user":
		return "tg block-user 1240314255 --allow-write --confirm 1240314255 --json"
	case "unblock-user":
		return "tg unblock-user 1240314255 --allow-write --confirm 1240314255 --json"
	case "leave-chat":
		return "tg leave-chat <group-chat-id> --allow-write --confirm <group-chat-id> --json"
	case "accounts-add":
		return "tg accounts-add work --json"
	case "accounts-list":
		return "tg accounts-list --json"
	case "accounts-show":
		return "tg accounts-show --json"
	case "accounts-use":
		return "tg accounts-use work --json"
	case "accounts-remove":
		return "tg accounts-remove work --json"
	case "import-telethon-session":
		return "tg import-telethon-session ~/path/to/tg.session --json"
	case "completion":
		return "tg completion zsh > ~/.zsh/completions/_tg"
	case "help":
		return "tg help send"
	case "stats":
		return "tg stats --json"
	case "contacts":
		return "tg contacts --json"
	case "unread":
		return "tg unread --json"
	}

	use = strings.TrimPrefix(use, "tg ")
	parts := strings.Fields(use)
	if len(parts) == 0 {
		return "tg " + name + " --help"
	}
	return "tg " + strings.Join(parts, " ") + " --json"
}

func anchor(name string) string {
	return strings.ReplaceAll(name, "-", "-")
}

func escapePipes(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
