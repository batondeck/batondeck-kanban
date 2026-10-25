package web

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"slices"
	"strings"
	"testing"

	webdist "github.com/batondeck/batondeck-kanban/web"
)

// TestAppCSPHashesTheInlineScripts recomputes the hash of every inline <script> in the embedded
// index.html: a rebuilt bootstrap that AppCSP does not name would be refused in the browser.
func TestAppCSPHashesTheInlineScripts(t *testing.T) {
	html, err := webdist.Dist.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	// Every <script> whatever its attributes; one with a src is not inline and needs no hash.
	var want []string
	for _, m := range regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`).FindAllSubmatch(html, -1) {
		if regexp.MustCompile(`(?i)\bsrc\s*=`).Match(m[1]) {
			continue
		}
		sum := sha256.Sum256(m[2])
		want = append(want, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	if len(want) == 0 {
		t.Fatal("no inline <script> in dist/index.html: drop its hash from AppCSP")
	}
	script := regexp.MustCompile(`script-src ([^;]*)`).FindStringSubmatch(AppCSP)
	if got, w := script[1], "'self' "+strings.Join(want, " "); got != w {
		t.Errorf("script-src %q, want %q", got, w)
	}
}

// TestAppCSPAdmitsTheFaviconHosts: the favicon proxy answers a redirect to gstatic, and img-src
// checks the redirect target, so both hosts must be named for a link card's favicon to load.
func TestAppCSPAdmitsTheFaviconHosts(t *testing.T) {
	img := strings.Fields(regexp.MustCompile(`img-src ([^;]*)`).FindStringSubmatch(AppCSP)[1])
	for _, host := range []string{"https://www.google.com", "https://*.gstatic.com"} {
		if !slices.Contains(img, host) {
			t.Errorf("img-src %q does not admit %s", img, host)
		}
	}
}
