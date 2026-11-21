package backend

import "testing"

func TestLoginIsResumable(t *testing.T) {
	hex := "0102030405060708090a0b0c0d0e0f100102030405060708090a0b0c0d0e0f10"
	cases := map[string]bool{
		"amber:com.greenart7c3.amber:" + hex:                             true,
		" amber:com.greenart7c3.amber:" + hex + " ":                      true,
		"bunker://abc?relay=wss%3A%2F%2Fr.example&secret=s3":             false,
		"BUNKER://abc?relay=wss%3A%2F%2Fr.example&secret=s3":             false,
		" bunker://abc?relay=wss%3A%2F%2Fr.example&secret=s3":            false,
		"nsec1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqz":    false,
		"NSEC1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQZ":    false,
		" nsec1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqz":   false,
		"ncryptsec1qgg9947rlpvqu76pj5ecreduf9jxhselq2nae2kghhvd5g7dgjtc": false,
		hex:       false,
		" " + hex: false,
		"":        false,
		"notakey": false,
	}
	for in, want := range cases {
		if got := loginIsResumable(in); got != want {
			t.Errorf("loginIsResumable(%q) = %v, want %v", in, got, want)
		}
	}
}
