/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	. "github.com/badu/http"
	"github.com/badu/http/hdr"
)

func TestHeaderWrite(t *testing.T) {
	var buf bytes.Buffer

	var headerWriteTests = []struct {
		h        hdr.Header
		exclude  map[string]bool
		expected string
	}{
		{hdr.Header{}, nil, ""},
		{
			hdr.Header{
				hdr.ContentType:   {"text/html; charset=UTF-8"},
				hdr.ContentLength: {"0"},
			},
			nil,
			"Content-Length: 0\r\nContent-Type: text/html; charset=UTF-8\r\n",
		},
		{
			hdr.Header{
				hdr.ContentLength: {"0", "1", "2"},
			},
			nil,
			"Content-Length: 0\r\nContent-Length: 1\r\nContent-Length: 2\r\n",
		},
		{
			hdr.Header{
				hdr.Expires:         {"-1"},
				hdr.ContentLength:   {"0"},
				hdr.ContentEncoding: {"gzip"},
			},
			map[string]bool{hdr.ContentLength: true},
			"Content-Encoding: gzip\r\nExpires: -1\r\n",
		},
		{
			hdr.Header{
				hdr.Expires:         {"-1"},
				hdr.ContentLength:   {"0", "1", "2"},
				hdr.ContentEncoding: {"gzip"},
			},
			map[string]bool{hdr.ContentLength: true},
			"Content-Encoding: gzip\r\nExpires: -1\r\n",
		},
		{
			hdr.Header{
				hdr.Expires:         {"-1"},
				hdr.ContentLength:   {"0"},
				hdr.ContentEncoding: {"gzip"},
			},
			map[string]bool{hdr.ContentLength: true, hdr.Expires: true, hdr.ContentEncoding: true},
			"",
		},
		{
			hdr.Header{
				"Nil":          nil,
				"Empty":        {},
				"Blank":        {""},
				"Double-Blank": {"", ""},
			},
			nil,
			"Blank: \r\nDouble-Blank: \r\nDouble-Blank: \r\n",
		},
		// Tests header sorting when over the insertion sort threshold side:
		{
			hdr.Header{
				"k1": {"1a", "1b"},
				"k2": {"2a", "2b"},
				"k3": {"3a", "3b"},
				"k4": {"4a", "4b"},
				"k5": {"5a", "5b"},
				"k6": {"6a", "6b"},
				"k7": {"7a", "7b"},
				"k8": {"8a", "8b"},
				"k9": {"9a", "9b"},
			},
			map[string]bool{"k5": true},
			"k1: 1a\r\nk1: 1b\r\nk2: 2a\r\nk2: 2b\r\nk3: 3a\r\nk3: 3b\r\n" +
				"k4: 4a\r\nk4: 4b\r\nk6: 6a\r\nk6: 6b\r\n" +
				"k7: 7a\r\nk7: 7b\r\nk8: 8a\r\nk8: 8b\r\nk9: 9a\r\nk9: 9b\r\n",
		},
	}

	for i, test := range headerWriteTests {
		test.h.WriteSubset(&buf, test.exclude)
		if buf.String() != test.expected {
			t.Errorf("#%d:\n got: %q\nwant: %q", i, buf.String(), test.expected)
		}
		buf.Reset()
	}
}

func TestParseTime(t *testing.T) {
	var parseTimeTests = []struct {
		h   hdr.Header
		err bool
	}{
		{hdr.Header{hdr.Date: {""}}, true},
		{hdr.Header{hdr.Date: {"invalid"}}, true},
		{hdr.Header{hdr.Date: {"1994-11-06T08:49:37Z00:00"}}, true},
		{hdr.Header{hdr.Date: {"Sun, 06 Nov 1994 08:49:37 GMT"}}, false},
		{hdr.Header{hdr.Date: {"Sunday, 06-Nov-94 08:49:37 GMT"}}, false},
		{hdr.Header{hdr.Date: {"Sun Nov  6 08:49:37 1994"}}, false},
	}

	expect := time.Date(1994, 11, 6, 8, 49, 37, 0, time.UTC)
	for i, test := range parseTimeTests {
		d, err := hdr.ParseTime(test.h.Get(hdr.Date))
		if err != nil {
			if !test.err {
				t.Errorf("#%d:\n got err: %v", i, err)
			}
			continue
		}
		if test.err {
			t.Errorf("#%d:\n  should err", i)
			continue
		}
		if !expect.Equal(d) {
			t.Errorf("#%d:\n got: %v\nwant: %v", i, d, expect)
		}
	}
}

func TestHasToken(t *testing.T) {

	type hasTokenTest struct {
		header string
		token  string
		want   bool
	}

	var hasTokenTests = []hasTokenTest{
		{"", "", false},
		{"", "foo", false},
		{"foo", "foo", true},
		{"foo ", "foo", true},
		{" foo", "foo", true},
		{" foo ", "foo", true},
		{"foo,bar", "foo", true},
		{"bar,foo", "foo", true},
		{"bar, foo", "foo", true},
		{"bar,foo, baz", "foo", true},
		{"bar, foo,baz", "foo", true},
		{"bar,foo, baz", "foo", true},
		{"bar, foo, baz", "foo", true},
		{"FOO", "foo", true},
		{"FOO ", "foo", true},
		{" FOO", "foo", true},
		{" FOO ", "foo", true},
		{"FOO,BAR", "foo", true},
		{"BAR,FOO", "foo", true},
		{"BAR, FOO", "foo", true},
		{"BAR,FOO, baz", "foo", true},
		{"BAR, FOO,BAZ", "foo", true},
		{"BAR,FOO, BAZ", "foo", true},
		{"BAR, FOO, BAZ", "foo", true},
		{"foobar", "foo", false},
		{"barfoo ", "foo", false},
	}

	for _, tt := range hasTokenTests {
		if HasToken(tt.header, tt.token) != tt.want {
			t.Errorf("hasToken(%q, %q) = %v; want %v", tt.header, tt.token, !tt.want, tt.want)
		}
	}
}

func TestHeaderWriteSubsetAllocs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping alloc test in short mode")
	}
	if raceEnabled {
		t.Skip("skipping test under race detector")
	} else {
		t.Log("TestHeaderWriteSubsetAllocs racing")
	}
	if runtime.GOMAXPROCS(0) > 1 {
		t.Skip("skipping; GOMAXPROCS>1")
	}

	var buf bytes.Buffer
	var testHeader = hdr.Header{
		hdr.ContentLength: {"123"},
		hdr.ContentType:   {"text/plain"},
		hdr.Date:          {"some date at some time Z"},
		hdr.ServerHeader:  {DefaultUserAgent},
	}

	n := testing.AllocsPerRun(100, func() {

		buf.Reset()
		testHeader.WriteSubset(&buf, nil)
	})
	if n > 0 {
		t.Errorf("allocs = %g; want 0", n)
	}
}
