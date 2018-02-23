/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bufio"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	. "http"
)

type canonicalHeaderKeyTest struct {
	in, out string
}

var canonicalHeaderKeyTests = []canonicalHeaderKeyTest{
	{"a-b-c", "A-B-C"},
	{"a-1-c", "A-1-C"},
	{"User-Agent", "User-Agent"},
	{"uSER-aGENT", "User-Agent"},
	{"user-agent", "User-Agent"},
	{"USER-AGENT", "User-Agent"},

	// Other valid tchar bytes in tokens:
	{"foo-bar_baz", "Foo-Bar_baz"},
	{"foo-bar$baz", "Foo-Bar$baz"},
	{"foo-bar~baz", "Foo-Bar~baz"},
	{"foo-bar*baz", "Foo-Bar*baz"},

	// Non-ASCII or anything with spaces or non-token chars is unchanged:
	{"üser-agenT", "üser-agenT"},
	{"a B", "a B"},

	// This caused a panic due to mishandling of a space:
	{"C Ontent-Transfer-Encoding", "C Ontent-Transfer-Encoding"},
	{"foo bar", "foo bar"},
}

func TestCanonicalMIMEHeaderKey(t *testing.T) {
	for _, tt := range canonicalHeaderKeyTests {
		if s := CanonicalHeaderKey(tt.in); s != tt.out {
			t.Errorf("CanonicalHeaderKey(%q) = %q, want %q", tt.in, s, tt.out)
		}
	}
}

func reader(s string) *HeaderReader {
	return NewHeaderReader(bufio.NewReader(strings.NewReader(s)))
}

func TestReadLine(t *testing.T) {
	r := reader("line1\nline2\n")
	s, err := r.ReadLine()
	if s != "line1" || err != nil {
		t.Fatalf("Line 1: %s, %v", s, err)
	}
	s, err = r.ReadLine()
	if s != "line2" || err != nil {
		t.Fatalf("Line 2: %s, %v", s, err)
	}
	s, err = r.ReadLine()
	if s != "" || err != io.EOF {
		t.Fatalf("EOF: %s, %v", s, err)
	}
}

func TestReadMIMEHeader(t *testing.T) {
	r := reader("my-key: Value 1  \r\nLong-key: Even \n Longer Value\r\nmy-Key: Value 2\r\n\n")
	m, err := r.ReadHeader()
	want := Header{
		"My-Key":   {"Value 1", "Value 2"},
		"Long-Key": {"Even Longer Value"},
	}
	if !reflect.DeepEqual(m, want) || err != nil {
		t.Fatalf("ReadHeader: %v, %v; want %v", m, err, want)
	}
}

func TestReadMIMEHeaderSingle(t *testing.T) {
	r := reader("Foo: bar\n\n")
	m, err := r.ReadHeader()
	want := Header{"Foo": {"bar"}}
	if !reflect.DeepEqual(m, want) || err != nil {
		t.Fatalf("ReadHeader: %v, %v; want %v", m, err, want)
	}
}

func TestReadMIMEHeaderNoKey(t *testing.T) {
	r := reader(": bar\ntest-1: 1\n\n")
	m, err := r.ReadHeader()
	want := Header{"Test-1": {"1"}}
	if !reflect.DeepEqual(m, want) || err != nil {
		t.Fatalf("ReadHeader: %v, %v; want %v", m, err, want)
	}
}

func TestLargeReadMIMEHeader(t *testing.T) {
	data := make([]byte, 16*1024)
	for i := 0; i < len(data); i++ {
		data[i] = 'x'
	}
	sdata := string(data)
	r := reader("Cookie: " + sdata + "\r\n\n")
	m, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	cookie := m.Get(CookieHeader)
	if cookie != sdata {
		t.Fatalf("ReadHeader: %v bytes, want %v bytes", len(cookie), len(sdata))
	}
}

// Test that we read slightly-bogus MIME headers seen in the wild,
// with spaces before colons, and spaces in keys.
func TestReadMIMEHeaderNonCompliant(t *testing.T) {
	// Invalid HTTP response header as sent by an Axis security
	// camera: (this is handled by IE, Firefox, Chrome, curl, etc.)
	r := reader("Foo: bar\r\n" +
		"Content-Language: en\r\n" +
		"SID : 0\r\n" +
		"Audio Mode : None\r\n" +
		"Privilege : 127\r\n\r\n")
	m, err := r.ReadHeader()
	want := Header{
		"Foo":           {"bar"},
		ContentLanguage: {"en"},
		"Sid":           {"0"},
		"Audio Mode":    {"None"},
		"Privilege":     {"127"},
	}
	if !reflect.DeepEqual(m, want) || err != nil {
		t.Fatalf("ReadHeader =\n%v, %v; want:\n%v", m, err, want)
	}
}

func TestReadMIMEHeaderMalformed(t *testing.T) {
	inputs := []string{
		"No colon first line\r\nFoo: foo\r\n\r\n",
		" No colon first line with leading space\r\nFoo: foo\r\n\r\n",
		"\tNo colon first line with leading tab\r\nFoo: foo\r\n\r\n",
		" First: line with leading space\r\nFoo: foo\r\n\r\n",
		"\tFirst: line with leading tab\r\nFoo: foo\r\n\r\n",
		"Foo: foo\r\nNo colon second line\r\n\r\n",
	}

	for _, input := range inputs {
		r := reader(input)
		if m, err := r.ReadHeader(); err == nil {
			t.Errorf("ReadHeader(%q) = %v, %v; want nil, err", input, m, err)
		}
	}
}

// Test that continued lines are properly trimmed. Issue 11204.
func TestReadMIMEHeaderTrimContinued(t *testing.T) {
	// In this header, \n and \r\n terminated lines are mixed on purpose.
	// We expect each line to be trimmed (prefix and suffix) before being concatenated.
	// Keep the spaces as they are.
	r := reader("" + // for code formatting purpose.
		"a:\n" +
		" 0 \r\n" +
		"b:1 \t\r\n" +
		"c: 2\r\n" +
		" 3\t\n" +
		"  \t 4  \r\n\n")
	m, err := r.ReadHeader()
	if err != nil {
		t.Fatal(err)
	}
	want := Header{
		"A": {"0"},
		"B": {"1"},
		"C": {"2 3 4"},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("ReadHeader mismatch.\n got: %q\nwant: %q", m, want)
	}
}

type readResponseTest struct {
	in       string
	inCode   int
	wantCode int
	wantMsg  string
}

var readResponseTests = []readResponseTest{
	{"230-Anonymous access granted, restrictions apply\n" +
		"Read the file README.txt,\n" +
		"230  please",
		23,
		230,
		"Anonymous access granted, restrictions apply\nRead the file README.txt,\n please",
	},

	{"230 Anonymous access granted, restrictions apply\n",
		23,
		230,
		"Anonymous access granted, restrictions apply",
	},

	{"400-A\n400-B\n400 C",
		4,
		400,
		"A\nB\nC",
	},

	{"400-A\r\n400-B\r\n400 C\r\n",
		4,
		400,
		"A\nB\nC",
	},
}

//TODO : @badu - recover this test
/**
func TestCommonHeaders(t *testing.T) {
	for h := range commonHeader {
		if h != CanonicalHeaderKey(h) {
			t.Errorf("Non-canonical header %q in commonHeader", h)
		}
	}
	b := []byte("content-Length")
	want := ContentLength
	n := testing.AllocsPerRun(200, func() {
		if x := canonicalMIMEHeaderKey(b); x != want {
			t.Fatalf("canonicalMIMEHeaderKey(%q) = %q; want %q", b, x, want)
		}
	})
	if n > 0 {
		t.Errorf("canonicalMIMEHeaderKey allocs = %v; want 0", n)
	}
}
**/
var clientHeaders = strings.Replace(`Host: golang.org
Connection: keep-alive
Cache-Control: max-age=0
Accept: application/xml,application/xhtml+xml,text/html;q=0.9,text/plain;q=0.8,image/png,*/*;q=0.5
User-Agent: Mozilla/5.0 (X11; U; Linux x86_64; en-US) AppleWebKit/534.3 (KHTML, like Gecko) Chrome/6.0.472.63 Safari/534.3
Accept-Encoding: gzip,deflate,sdch
Accept-Language: en-US,en;q=0.8,fr-CH;q=0.6
Accept-Charset: ISO-8859-1,utf-8;q=0.7,*;q=0.3
COOKIE: __utma=000000000.0000000000.0000000000.0000000000.0000000000.00; __utmb=000000000.0.00.0000000000; __utmc=000000000; __utmz=000000000.0000000000.00.0.utmcsr=code.google.com|utmccn=(referral)|utmcmd=referral|utmcct=/p/go/issues/detail
Non-Interned: test

`, "\n", "\r\n", -1)

var serverHeaders = strings.Replace(`Content-Type: text/html; charset=utf-8
Content-Encoding: gzip
Date: Thu, 27 Sep 2012 09:03:33 GMT
Server: Google Frontend
Cache-Control: private
Content-Length: 2298
VIA: 1.1 proxy.example.com:80 (XXX/n.n.n-nnn)
Connection: Close
Non-Interned: test

`, "\n", "\r\n", -1)

func BenchmarkReadMIMEHeader(b *testing.B) {
	b.ReportAllocs()
	var buf bytes.Buffer
	br := bufio.NewReader(&buf)
	r := NewHeaderReader(br)
	for i := 0; i < b.N; i++ {
		var want int
		var find string
		if (i & 1) == 1 {
			buf.WriteString(clientHeaders)
			want = 10
			find = CookieHeader
		} else {
			buf.WriteString(serverHeaders)
			want = 9
			find = Via
		}
		h, err := r.ReadHeader()
		if err != nil {
			b.Fatal(err)
		}
		if len(h) != want {
			b.Fatalf("wrong number of headers: got %d, want %d", len(h), want)
		}
		if _, ok := h[find]; !ok {
			b.Fatalf("did not find key %s", find)
		}
	}
}

func BenchmarkUncommon(b *testing.B) {
	b.ReportAllocs()
	var buf bytes.Buffer
	br := bufio.NewReader(&buf)
	r := NewHeaderReader(br)
	for i := 0; i < b.N; i++ {
		buf.WriteString("uncommon-header-for-benchmark: foo\r\n\r\n")
		h, err := r.ReadHeader()
		if err != nil {
			b.Fatal(err)
		}
		if _, ok := h["Uncommon-Header-For-Benchmark"]; !ok {
			b.Fatal("Missing result header.")
		}
	}
}
