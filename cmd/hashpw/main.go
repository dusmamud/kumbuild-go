package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	pw := os.Getenv("PW")
	if pw == "" {
		fmt.Fprint(os.Stderr, "Enter password to hash: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading password: %v\n", err)
			os.Exit(1)
		}
		pw = strings.TrimSpace(input)
	}

	if pw == "" {
		fmt.Fprintln(os.Stderr, "Password cannot be empty")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating bcrypt hash: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(hash))
}
