package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	tmplAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	classAttr  = regexp.MustCompile(`class="([^"%]*)"`)
	styleAttr  = regexp.MustCompile(`style="([^"]*)"`)
	customProp = regexp.MustCompile(`^(--[a-z-]+:[^;]*;?)+$`)
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssBlock   = regexp.MustCompile(`\{[^{}]*\}`)
	cssClass   = regexp.MustCompile(`\.([a-zA-Z][\w-]*)`)
)

// The markup is styled by spoor.css alone: every class a template or a Go
// renderer writes is defined there, and a style attribute may only set
// custom properties, which is how computed geometry reaches a class.
func TestMarkupUsesOnlyTheStylesheet(t *testing.T) {
	css, err := os.ReadFile("static/spoor.css")
	if err != nil {
		t.Fatal(err)
	}
	selectors := cssBlock.ReplaceAllString(cssComment.ReplaceAllString(string(css), ""), "")
	defined := map[string]bool{}
	for _, m := range cssClass.FindAllStringSubmatch(selectors, -1) {
		defined[m[1]] = true
	}
	known := func(class string) bool {
		if !strings.HasSuffix(class, "-") { // "k-" is the fixed half of class="k-{{.Kind}}"
			return defined[class]
		}
		for d := range defined {
			if strings.HasPrefix(d, class) {
				return true
			}
		}
		return false
	}

	files, _ := filepath.Glob("templates/*.html")
	goFiles, _ := filepath.Glob("*.go")
	for _, f := range append(files, goFiles...) {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f) // #nosec G304 -- the package's own sources
		if err != nil {
			t.Fatal(err)
		}
		src := tmplAction.ReplaceAllString(string(raw), " ")
		for _, m := range classAttr.FindAllStringSubmatch(src, -1) {
			for _, class := range strings.Fields(m[1]) {
				if !known(class) {
					t.Errorf("%s: class %q is not defined in spoor.css", f, class)
				}
			}
		}
		checkInlineStyle(t, f, src)
	}
}

func checkInlineStyle(t *testing.T, f, src string) {
	for _, m := range styleAttr.FindAllStringSubmatch(src, -1) {
		if !customProp.MatchString(m[1]) {
			t.Errorf("%s: inline style %q; only custom properties are allowed", f, m[1])
		}
	}
	if strings.Contains(src, "<style") && f != "export.go" {
		t.Errorf("%s: a <style> element; the stylesheet is spoor.css", f)
	}
}
