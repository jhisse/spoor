package web

import (
	"net/http"
	"net/url"
	"strings"
)

const themeCookie = "spoor_theme"

// themeOf is the theme the reader chose: "" when none, or when the cookie
// holds anything else, and the page then follows the system.
func themeOf(r *http.Request) string {
	if c, err := r.Cookie(themeCookie); err == nil && (c.Value == "light" || c.Value == "dark") {
		return c.Value
	}
	return ""
}

// Theme handles POST /theme, the header's three buttons: "light" and "dark"
// are kept in a cookie the layout reads (no script, no flash), "system"
// deletes it, anything else changes nothing. It answers with the page the
// reader was on.
func Theme(w http.ResponseWriter, r *http.Request) {
	// #nosec G124 -- not Secure on purpose: the UI is plain HTTP on loopback, and this is a colour preference, not a credential
	c := &http.Cookie{Name: themeCookie, Path: "/", SameSite: http.SameSiteLaxMode, HttpOnly: true}
	switch v := r.PostFormValue("theme"); v {
	case "light", "dark":
		c.Value, c.MaxAge = v, 400*24*60*60 // the longest a browser keeps one
		http.SetCookie(w, c)
	case "system":
		c.MaxAge = -1
		http.SetCookie(w, c)
	}
	// Only the path of a same-host Referer is followed, and never one a
	// browser would read as another host ("//host").
	back := "/"
	if u, err := url.Parse(r.Referer()); err == nil && u.Host == r.Host && !strings.HasPrefix(u.Path, "//") {
		back = u.RequestURI()
	}
	http.Redirect(w, r, back, http.StatusSeeOther) // #nosec G710 -- back is "/" or the path of a Referer on this host, checked above
}
