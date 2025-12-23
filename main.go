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

	buf := make([]byte, 4096)
	readCh := make(chan string, 100)
	errCh := make(chan error, 1)
	done := make(chan struct{})

	// Start reader goroutine
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				n, err := stdout.Read(buf)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				readCh <- string(buf[:n])
			}
		}
	}()

	// Wait for initial prompt with timeout
	promptTimeout := time.After(5 * time.Second)
waitPrompt:
	for {
		select {
		case chunk := <-readCh:
			if strings.Contains(chunk, "#") {
				break waitPrompt
			}
		case <-promptTimeout:
			close(done)
			log.Fatal("Timeout waiting for prompt")
		case <-errCh:
			close(done)
			log.Fatal("Connection error")
		}
	}

	// Send command
	fmt.Fprintf(stdin, "%s\n", *command)

	// Read output with timeout
	var output strings.Builder
	timeout := time.After(30 * time.Second)
	lastRead := time.Now()
	seenContent := false

readLoop:
	for {
		select {
		case chunk := <-readCh:
			lastRead = time.Now()
			output.WriteString(chunk)

			// Handle pagination - send space to continue
			if strings.Contains(strings.ToLower(chunk), "more") {
				fmt.Fprintf(stdin, " ")
				continue
			}

			// Mark that we've received actual content (not just command echo)
			if strings.Contains(chunk, "\n") {
				seenContent = true
			}

			// Check if we've returned to prompt after seeing content
			if seenContent {
				trimmed := strings.TrimRight(output.String(), " \r\n")
				if strings.HasSuffix(trimmed, "#") {
					break readLoop
				}
			}

		case <-errCh:
			break readLoop

		case <-timeout:
			break readLoop

		default:
			// If no data for 500ms after last read, assume done
			if time.Since(lastRead) > 500*time.Millisecond && output.Len() > 0 {
				break readLoop
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	close(done)

	// Clean up output
	result := output.String()
	lines := strings.Split(result, "\n")

	// Skip first line (echo of command) and last line (prompt)
	if len(lines) > 2 {
		lines = lines[1 : len(lines)-1]
	}

	// Print cleaned output
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			fmt.Println(line)
		}
	}

	fmt.Fprintf(stdin, "exit\n")
}
