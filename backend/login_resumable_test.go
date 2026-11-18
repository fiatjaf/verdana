package backend

import "testing"

func TestLoginIsResumable(t *testing.T) {
	hex := "0102030405060708090a0b0c0d0e0f100102030405060708090a0b0c0d0e0f10"
	cases := map[string]bool{
		"amber:com.greenart7c3.amber:" + hex:                          true,
		"bunker://abc?relay=wss%3A%2F%2Fr.example&secret=s3":          false,
		"nsec1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqz": false,
		hex:       false,
		" " + hex: false,
		"":        true, // empty is not a secret; Login rejects it elsewhere
		"notakey": true,
	}
	for in, want := range cases {
		if got := loginIsResumable(in); got != want {
			t.Errorf("loginIsResumable(%q) = %v, want %v", in, got, want)
		}
	}
}
