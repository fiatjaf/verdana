package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// psSingleQuote wraps a string in a PowerShell single-quoted literal, doubling
// embedded single quotes and stripping control characters (including NUL/CR/LF).
func psSingleQuote(s string) string {
	cleaned := stripControlChars(s)
	return "'" + strings.ReplaceAll(cleaned, "'", "''") + "'"
}

// buildWindowsShortcutPS constructs the PowerShell script that writes a .lnk
// shortcut. Every interpolated argument uses psSingleQuote directly without
// outer quotes (wrapping psSingleQuote in '...' previously produced ''...''
// and left arbitrary tokens unquoted in PowerShell).
func buildWindowsShortcutPS(target, exe, token, iconPath string) string {
	iconStmt := ""
	if iconPath != "" {
		iconStmt = fmt.Sprintf("$s.IconLocation = %s; ", psSingleQuote(iconPath+",0"))
	}
	return fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut(%s); $s.TargetPath = %s; $s.Arguments = %s; %s$s.Save()`,
		psSingleQuote(target), psSingleQuote(exe), psSingleQuote(token), iconStmt,
	)
}

// encodePSCommand encodes a PowerShell script as UTF-16LE base64 for
// powershell.exe -EncodedCommand, avoiding command-line parser quoting quirks.
func encodePSCommand(script string) string {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// stripControlChars removes ASCII control characters (including CR, LF, NUL)
// and DEL so untrusted strings cannot break INI files or shell arguments.
func stripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
