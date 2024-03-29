// Copyright 2013 The Go Authors. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file or at
// https://developers.google.com/open-source/licenses/bsd.

package httputil_test

import (
	"net/http"
	"testing"

	"github.com/networkteam/vignet/httputil"
)

var negotiateContentTypeTests = []struct {
	s            string
	offers       []string
	defaultOffer string
	expect       string
}{
	{"text/html, */*;q=0", []string{"x/y"}, "", ""},
	{"text/html, */*", []string{"x/y"}, "", "x/y"},
	{"text/html, image/png", []string{"text/html", "image/png"}, "", "text/html"},
	{"text/html, image/png", []string{"image/png", "text/html"}, "", "image/png"},
	{"text/html, image/png; q=0.5", []string{"image/png"}, "", "image/png"},
	{"text/html, image/png; q=0.5", []string{"text/html"}, "", "text/html"},
	{"text/html, image/png; q=0.5", []string{"foo/bar"}, "", ""},
	{"text/html, image/png; q=0.5", []string{"image/png", "text/html"}, "", "text/html"},
	{"text/html, image/png; q=0.5", []string{"text/html", "image/png"}, "", "text/html"},
	{"text/html;q=0.5, image/png", []string{"image/png"}, "", "image/png"},
	{"text/html;q=0.5, image/png", []string{"text/html"}, "", "text/html"},
	{"text/html;q=0.5, image/png", []string{"image/png", "text/html"}, "", "image/png"},
	{"text/html;q=0.5, image/png", []string{"text/html", "image/png"}, "", "image/png"},
	{"image/png, image/*;q=0.5", []string{"image/jpg", "image/png"}, "", "image/png"},
	{"image/png, image/*;q=0.5", []string{"image/jpg"}, "", "image/jpg"},
	{"image/png, image/*;q=0.5", []string{"image/jpg", "image/gif"}, "", "image/jpg"},
	{"image/png, image/*", []string{"image/jpg", "image/gif"}, "", "image/jpg"},
	{"image/png, image/*", []string{"image/gif", "image/jpg"}, "", "image/gif"},
	{"image/png, image/*", []string{"image/gif", "image/png"}, "", "image/png"},
	{"image/png, image/*", []string{"image/png", "image/gif"}, "", "image/png"},
}

func TestNegotiateContentType(t *testing.T) {
	for _, tt := range negotiateContentTypeTests {
		r := &http.Request{Header: http.Header{"Accept": {tt.s}}}
		actual := httputil.NegotiateContentType(r, tt.offers, tt.defaultOffer)
		if actual != tt.expect {
			t.Errorf("NegotiateContentType(%q, %#v, %q)=%q, want %q", tt.s, tt.offers, tt.defaultOffer, actual, tt.expect)
		}
	}
}
