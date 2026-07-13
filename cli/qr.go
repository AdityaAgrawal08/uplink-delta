package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/skip2/go-qrcode"
	"golang.org/x/term"
)

func ShouldShowQR(configShowQR string) bool {
	if configShowQR == "false" {
		return false
	}
	if configShowQR == "true" {
		return true
	}

	// Default "auto" check
	// 1. Must be terminal
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}

	// 2. Terminal width must be >= 80 to prevent wrapping and distortion
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width < 80 {
		return false
	}

	// 3. Must support UTF-8 (check LANG)
	lang := strings.ToLower(os.Getenv("LANG"))
	if !strings.Contains(lang, "utf-8") && !strings.Contains(lang, "utf8") {
		return false
	}

	return true
}

func PrintQRCode(url string) {
	// Always use the dynamic minimal size chosen by the library for low recovery level
	qr, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		fmt.Printf("Error creating QR code: %v\n", err)
		return
	}

	// ToString(true) uses UTF-8 half-block characters inverted for dark backgrounds
	qrString := qr.ToString(true)
	fmt.Println(qrString)
	fmt.Println("  Scan with your phone camera to open")
	fmt.Println()
}
