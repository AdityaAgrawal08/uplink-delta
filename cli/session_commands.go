package main

import (
	"flag"
	"fmt"
	"os"
	"os/user"
	"time"

	"golang.org/x/term"
)

func handleSessionSubcommand(args []string) {
	if len(args) == 0 {
		printSessionUsage()
		os.Exit(1)
	}

	sub := args[0]
	switch sub {
	case "create":
		handleSessionCreate(args[1:])
	case "join":
		handleSessionJoin(args[1:])
	case "leave":
		handleSessionLeave()
	case "status":
		handleSessionStatus()
	default:
		fmt.Printf("✗ Error: Unknown session subcommand \"%s\"\n\n", sub)
		printSessionUsage()
		os.Exit(1)
	}
}

func printSessionUsage() {
	fmt.Println("Usage: uplink session <command> [arguments] [flags]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  create      Create a new collaborative session")
	fmt.Println("              uplink session create [--username name] [--password pwd] [--duration 15m]")
	fmt.Println()
	fmt.Println("  join        Join an existing session by ID")
	fmt.Println("              uplink session join <sessionId> [--username name] [--password pwd]")
	fmt.Println()
	fmt.Println("  leave       Leave the current active session")
	fmt.Println("              uplink session leave")
	fmt.Println()
	fmt.Println("  status      Show current active session details")
	fmt.Println("              uplink session status")
}

func getDefaultUsername() string {
	u, err := user.Current()
	if err == nil && u.Username != "" {
		return u.Username
	}
	envUser := os.Getenv("USER")
	if envUser != "" {
		return envUser
	}
	return "user"
}

func handleSessionCreate(args []string) {
	cfg := LoadConfig()

	createCmd := flag.NewFlagSet("session create", flag.ExitOnError)
	usernameFlag := createCmd.String("username", getDefaultUsername(), "Username for the session")
	passwordFlag := createCmd.String("password", "", "Password for the session")
	durationFlag := createCmd.String("duration", "10m", "Session duration (e.g. 10m, 30m, 1h)")
	serverFlag := createCmd.String("server", cfg.Server, "Server base URL")

	err := createCmd.Parse(args)
	if err != nil {
		fmt.Printf("✗ Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	durationSec, err := parseDurationToSeconds(*durationFlag)
	if err != nil {
		fmt.Printf("✗ Error parsing duration flag: %v\n", err)
		os.Exit(1)
	}

	serverUrl := sanitizeServerUrl(*serverFlag)
	username := *usernameFlag
	password := *passwordFlag

	// If username has invalid characters, sanitise it: only allow alphanumeric and underscores
	sanitizedUsername := ""
	for _, char := range username {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' {
			sanitizedUsername += string(char)
		}
	}
	if len(sanitizedUsername) < 3 {
		sanitizedUsername = "user_" + fmt.Sprintf("%d", time.Now().UnixNano()%10000)
	}
	if len(sanitizedUsername) > 20 {
		sanitizedUsername = sanitizedUsername[:20]
	}

	// Verify duration limits
	if durationSec < 60 {
		durationSec = 60
	}
	if durationSec > 3600 {
		durationSec = 3600
	}

	fmt.Printf("Creating session on %s...\n", serverUrl)
	sessionId, err := CreateSession(serverUrl, sanitizedUsername, password, durationSec)
	if err != nil {
		fmt.Printf("✗ Error creating session: %v\n", err)
		os.Exit(1)
	}

	sess := &ActiveSession{
		SessionId: sessionId,
		Username:  sanitizedUsername,
		Server:    serverUrl,
		JoinedAt:  time.Now(),
		Password:  password,
	}

	err = SaveActiveSession(sess)
	if err != nil {
		fmt.Printf("✗ Error saving session locally: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Session created! ID: %s\n", sessionId)
	fmt.Println("Launching TUI...")

	StartTUI(sess)
}

func handleSessionJoin(args []string) {
	cfg := LoadConfig()

	if len(args) < 1 {
		fmt.Println("✗ Error: Session ID is required to join.")
		fmt.Println("Usage: uplink session join <sessionId> [--username name] [--password pwd]")
		os.Exit(1)
	}

	sessionId := args[0]
	
	joinCmd := flag.NewFlagSet("session join", flag.ExitOnError)
	usernameFlag := joinCmd.String("username", getDefaultUsername(), "Username for the session")
	passwordFlag := joinCmd.String("password", "", "Password for the session (prompted if missing and required)")
	serverFlag := joinCmd.String("server", cfg.Server, "Server base URL")

	err := joinCmd.Parse(args[1:])
	if err != nil {
		fmt.Printf("✗ Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	serverUrl := sanitizeServerUrl(*serverFlag)
	username := *usernameFlag
	password := *passwordFlag

	sanitizedUsername := ""
	for _, char := range username {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' {
			sanitizedUsername += string(char)
		}
	}
	if len(sanitizedUsername) < 3 {
		sanitizedUsername = "user_" + fmt.Sprintf("%d", time.Now().UnixNano()%10000)
	}
	if len(sanitizedUsername) > 20 {
		sanitizedUsername = sanitizedUsername[:20]
	}

	fmt.Printf("Joining session %s on %s...\n", sessionId, serverUrl)
	participants, err := JoinSession(serverUrl, sessionId, sanitizedUsername, password)
	
	// Handle password prompting if 401 is returned
	if err != nil && (password == "") {
		fmt.Print("Enter session password: ")
		pwdBytes, pwdErr := term.ReadPassword(int(os.Stdin.Fd()))
		if pwdErr == nil {
			fmt.Println()
			password = string(pwdBytes)
			participants, err = JoinSession(serverUrl, sessionId, sanitizedUsername, password)
		}
	}

	if err != nil {
		fmt.Printf("✗ Error joining session: %v\n", err)
		os.Exit(1)
	}

	sess := &ActiveSession{
		SessionId: sessionId,
		Username:  sanitizedUsername,
		Server:    serverUrl,
		JoinedAt:  time.Now(),
		Password:  password,
	}

	err = SaveActiveSession(sess)
	if err != nil {
		fmt.Printf("✗ Error saving session locally: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Joined session! Active participants: %v\n", participants)
	fmt.Println("Launching TUI...")

	StartTUI(sess)
}

func handleSessionLeave() {
	_, err := LoadActiveSession()
	if err != nil {
		fmt.Println("No active session to leave.")
		return
	}

	err = DeleteActiveSession()
	if err != nil {
		fmt.Printf("✗ Error leaving session: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Left session successfully.")
}

func handleSessionStatus() {
	sess, err := LoadActiveSession()
	if err != nil {
		fmt.Println("No active session.")
		return
	}

	fmt.Println("Active Session:")
	fmt.Printf("  ID:       %s\n", sess.SessionId)
	fmt.Printf("  Username: %s\n", sess.Username)
	fmt.Printf("  Server:   %s\n", sess.Server)
	fmt.Printf("  Joined:   %s\n", sess.JoinedAt.Format(time.RFC822))
}
