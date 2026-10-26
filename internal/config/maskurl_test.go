package config

import "testing"

func TestMaskURLMasksCredentialsAndKeepsTheRest(t *testing.T) {
	const host = "wiki.example.invalid"
	for raw, want := range map[string]string{
		"https://wiki.example.invalid/api":  "https://wiki.example.invalid/api",
		"":                                  "",
		"https://user:pw@" + host + "/":     "https://***:***@wiki.example.invalid/",
		"https://onlyuser@" + host + "/api": "https://***@wiki.example.invalid/api",
		"https://wiki.example.invalid/api?token=abc&page=1":        "https://wiki.example.invalid/api?token=***&page=1",
		"https://wiki.example.invalid/?X-Amz-Signature=abc&Name=n": "https://wiki.example.invalid/?X-Amz-Signature=***&Name=n",
		"https://wiki.example.invalid/?flag&apikey=abc":            "https://wiki.example.invalid/?flag&apikey=***",
		"https://wiki.example.invalid/#section":                    "https://wiki.example.invalid/#section",
		"https://wiki.example.invalid/#token=abc":                  "https://wiki.example.invalid/#token=***",
		"#ff0000": "#ff0000",
		"https://wiki.example.invalid/?a=1#access_token=abc&state=x": "https://wiki.example.invalid/?a=1#access_token=***&state=x",
		"https://u:p@" + host + "/p?session=s#cookie=c":              "https://***:***@wiki.example.invalid/p?session=***#cookie=***",
		"wiki.example.invalid/path?token=abc":                        "***",
		"user:pw@" + host:                                            "***",
		"https://bad host@" + host + "/":                             "***",
		"https://wiki.example.invalid/api?page=1&q=a%20b":            "https://wiki.example.invalid/api?page=1&q=a%20b",
		"https://wiki.example.invalid/api?":                          "https://wiki.example.invalid/api?",
	} {
		if got := MaskURL(raw); got != want {
			t.Errorf("MaskURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
