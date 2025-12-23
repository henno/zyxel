package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/crypto/ssh"
)

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

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
		fmt.Println()
		fmt.Println("Environment variables:")
		fmt.Println("  ZYXEL_HOST      Switch IP address (required)")
		fmt.Println("  ZYXEL_USER      SSH username (required)")
		fmt.Println("  ZYXEL_PASSWORD  SSH password (required)")
		fmt.Println("  ZYXEL_PORT      SSH port (default: 22)")
		os.Exit(1)
	}

	// Load .env if present
	_ = godotenv.Load()

	host := os.Getenv("ZYXEL_HOST")
	user := os.Getenv("ZYXEL_USER")
	password := os.Getenv("ZYXEL_PASSWORD")
	port := os.Getenv("ZYXEL_PORT")

	var missing []string
	if host == "" {
		missing = append(missing, "ZYXEL_HOST")
	}
	if user == "" {
		missing = append(missing, "ZYXEL_USER")
	}
	if password == "" {
		missing = append(missing, "ZYXEL_PASSWORD")
	}
	if len(missing) > 0 {
		fatal("Missing required environment variables: %s", strings.Join(missing, ", "))
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
		fatal("Failed to connect to %s: %v", address, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		fatal("Failed to create SSH session: %v", err)
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if err := session.RequestPty("xterm", 80, 200, modes); err != nil {
		fatal("Failed to request PTY: %v", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		fatal("Failed to get stdin pipe: %v", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		fatal("Failed to get stdout pipe: %v", err)
	}

	if err := session.Shell(); err != nil {
		fatal("Failed to start shell: %v", err)
	}

	buf := make([]byte, 4096)
	readCh := make(chan string, 100)
	errCh := make(chan error, 1)
	done := make(chan struct{})

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

	// Wait for initial prompt
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
			fatal("Timeout waiting for switch prompt")
		case <-errCh:
			close(done)
			fatal("Connection closed unexpectedly")
		}
	}

	// Send command
	fmt.Fprintf(stdin, "%s\n", *command)

	// Read output
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

			if strings.Contains(strings.ToLower(chunk), "more") {
				fmt.Fprintf(stdin, " ")
				continue
			}

			if strings.Contains(chunk, "\n") {
				seenContent = true
			}

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
			if time.Since(lastRead) > 500*time.Millisecond && output.Len() > 0 {
				break readLoop
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	close(done)

	// Clean and print output
	result := output.String()
	lines := strings.Split(result, "\n")

	if len(lines) > 2 {
		lines = lines[1 : len(lines)-1]
	}

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			fmt.Println(line)
		}
	}

	fmt.Fprintf(stdin, "exit\n")
}
