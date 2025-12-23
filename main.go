package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/crypto/ssh"
)

func main() {
	command := flag.String("c", "", "Zyxel command to execute")
	flag.Parse()

	if *command == "" {
		fmt.Println("Usage: zyxel -c '<command>'")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  zyxel -c 'show system-information'")
		fmt.Println("  zyxel -c 'show running-config'")
		fmt.Println("  zyxel -c 'show interface *'")
		fmt.Println("  zyxel -c 'show mac address-table'")
		fmt.Println("  zyxel -c 'show vlan'")
		fmt.Println("  zyxel -c '?'                        # show available commands")
		os.Exit(1)
	}

	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	host := os.Getenv("ZYXEL_HOST")
	user := os.Getenv("ZYXEL_USER")
	password := os.Getenv("ZYXEL_PASSWORD")
	port := os.Getenv("ZYXEL_PORT")

	if host == "" || user == "" || password == "" {
		log.Fatal("ZYXEL_HOST, ZYXEL_USER, and ZYXEL_PASSWORD must be set in .env")
	}

	if port == "" {
		port = "22"
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = password
				}
				return answers, nil
			}),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		HostKeyAlgorithms: []string{
			"ssh-rsa",
			"rsa-sha2-256",
			"rsa-sha2-512",
		},
		Config: ssh.Config{
			KeyExchanges: []string{
				"diffie-hellman-group-exchange-sha256",
				"diffie-hellman-group14-sha256",
				"diffie-hellman-group14-sha1",
			},
		},
		Timeout: 10 * time.Second,
	}

	address := fmt.Sprintf("%s:%s", host, port)
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}
	defer session.Close()

	// Set up terminal modes for interactive session
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if err := session.RequestPty("xterm", 80, 200, modes); err != nil {
		log.Fatalf("Failed to request PTY: %v", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		log.Fatalf("Failed to get stdin: %v", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		log.Fatalf("Failed to get stdout: %v", err)
	}

	if err := session.Shell(); err != nil {
		log.Fatalf("Failed to start shell: %v", err)
	}

	// Wait for prompt
	buf := make([]byte, 4096)
	for {
		n, err := stdout.Read(buf)
		if err != nil {
			break
		}
		if strings.Contains(string(buf[:n]), "#") {
			break
		}
	}

	// Send command
	fmt.Fprintf(stdin, "%s\n", *command)

	// Read output until we see the prompt again
	var output strings.Builder
	for {
		n, err := stdout.Read(buf)
		if err != nil {
			break
		}
		chunk := string(buf[:n])
		output.WriteString(chunk)

		// Handle pagination - send space to continue
		if strings.Contains(chunk, "More") || strings.Contains(chunk, "more") {
			fmt.Fprintf(stdin, " ")
			continue
		}

		// Check if we've returned to prompt
		if strings.HasSuffix(strings.TrimSpace(chunk), "#") {
			time.Sleep(100 * time.Millisecond)
			break
		}
	}

	// Clean up output
	result := output.String()
	lines := strings.Split(result, "\n")

	// Skip first line (echo of command) and last line (prompt)
	if len(lines) > 2 {
		lines = lines[1 : len(lines)-1]
	}

	// Print cleaned output
	for _, line := range lines {
		// Remove carriage returns and trim
		line = strings.TrimRight(line, "\r")
		if line != "" {
			fmt.Println(line)
		}
	}

	fmt.Fprintf(stdin, "exit\n")
}
