/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/badu/http/hdr"
	"github.com/badu/http/mime"
)

const (
	// This constant needs to be at least 76 for this package to work correctly.
	// This is because \r\n--separator_of_len_70- would fill the buffer and it wouldn't be safe to consume a single byte from it.
	peekBufferSize = 4096
)

func TestWriter(t *testing.T) {
	fileContents := []byte("my file contents")

	var b bytes.Buffer
	w := mime.NewMultipartWriter(&b)
	{
		part, err := w.CreateFormFile("myfile", "my-file.txt")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		part.Write(fileContents)
		err = w.WriteField("key", "val")
		if err != nil {
			t.Fatalf("WriteField: %v", err)
		}
		part.Write([]byte("val"))
		err = w.Close()
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
		s := b.String()
		if len(s) == 0 {
			t.Fatal("String: unexpected empty result")
		}
		if s[0] == '\r' || s[0] == '\n' {
			t.Fatal("String: unexpected newline")
		}
	}

	r := mime.NewMultipartReader(&b, w.Boundary())

	part, err := r.NextPart()
	if err != nil {
		t.Fatalf("part 1: %v", err)
	}
	if g, e := part.FormName(), "myfile"; g != e {
		t.Errorf("part 1: want form name %q, got %q", e, g)
	}
	slurp, err := ioutil.ReadAll(part)
	if err != nil {
		t.Fatalf("part 1: ReadAll: %v", err)
	}
	if e, g := string(fileContents), string(slurp); e != g {
		t.Errorf("part 1: want contents %q, got %q", e, g)
	}

	part, err = r.NextPart()
	if err != nil {
		t.Fatalf("part 2: %v", err)
	}
	if g, e := part.FormName(), "key"; g != e {
		t.Errorf("part 2: want form name %q, got %q", e, g)
	}
	slurp, err = ioutil.ReadAll(part)
	if err != nil {
		t.Fatalf("part 2: ReadAll: %v", err)
	}
	if e, g := "val", string(slurp); e != g {
		t.Errorf("part 2: want contents %q, got %q", e, g)
	}

	part, err = r.NextPart()
	if part != nil || err == nil {
		t.Fatalf("expected end of parts; got %v, %v", part, err)
	}
}

func TestWriterSetBoundary(t *testing.T) {
	tests := []struct {
		b  string
		ok bool
	}{
		{"abc", true},
		{"", false},
		{"ungültig", false},
		{"!", false},
		{strings.Repeat("x", 70), true},
		{strings.Repeat("x", 71), false},
		{"bad!ascii!", false},
		{"my-separator", true},
		{"with space", true},
		{"badspace ", false},
	}
	for i, tt := range tests {
		var b bytes.Buffer
		w := mime.NewMultipartWriter(&b)
		err := w.SetBoundary(tt.b)
		got := err == nil
		if got != tt.ok {
			t.Errorf("%d. boundary %q = %v (%v); want %v", i, tt.b, got, err, tt.ok)
		} else if tt.ok {
			got := w.Boundary()
			if got != tt.b {
				t.Errorf("boundary = %q; want %q", got, tt.b)
			}
			w.Close()
			wantSub := "\r\n--" + tt.b + "--\r\n"
			if got := b.String(); !strings.Contains(got, wantSub) {
				t.Errorf("expected %q in output. got: %q", wantSub, got)
			}
		}
	}
}

func TestWriterBoundaryGoroutines(t *testing.T) {
	// Verify there's no data race accessing any lazy boundary if it's used by
	// different goroutines. This was previously broken by
	// https://codereview.appspot.com/95760043/ and reverted in
	// https://codereview.appspot.com/117600043/
	w := mime.NewMultipartWriter(ioutil.Discard)
	done := make(chan int)
	go func() {
		w.CreateFormField("foo")
		done <- 1
	}()
	w.Boundary()
	<-done
}

func TestSortedHeader(t *testing.T) {
	var buf bytes.Buffer
	w := mime.NewMultipartWriter(&buf)
	if err := w.SetBoundary("MIMEBOUNDARY"); err != nil {
		t.Fatalf("Error setting mime boundary: %v", err)
	}

	header := hdr.Header{
		"A": {"2"},
		"B": {"5", "7", "6"},
		"C": {"4"},
		"M": {"3"},
		"Z": {"1"},
	}

	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatalf("Unable to create part: %v", err)
	}
	part.Write([]byte("foo"))

	w.Close()

	want := "--MIMEBOUNDARY\r\nA: 2\r\nB: 5\r\nB: 7\r\nB: 6\r\nC: 4\r\nM: 3\r\nZ: 1\r\n\r\nfoo\r\n--MIMEBOUNDARY--\r\n"
	if want != buf.String() {
		t.Fatalf("\n got: %q\nwant: %q\n", buf.String(), want)
	}
}

func TestBoundaryLine(t *testing.T) {
	mr := mime.NewMultipartReader(strings.NewReader(""), "myBoundary")
	if !mr.IsBoundaryDelimiterLine([]byte("--myBoundary\r\n")) {
		t.Error("expected")
	}
	if !mr.IsBoundaryDelimiterLine([]byte("--myBoundary \r\n")) {
		t.Error("expected")
	}
	if !mr.IsBoundaryDelimiterLine([]byte("--myBoundary \n")) {
		t.Error("expected")
	}
	if mr.IsBoundaryDelimiterLine([]byte("--myBoundary bogus \n")) {
		t.Error("expected fail")
	}
	if mr.IsBoundaryDelimiterLine([]byte("--myBoundary bogus--")) {
		t.Error("expected fail")
	}
}

func escapeString(v string) string {
	bytes, _ := json.Marshal(v)
	return string(bytes)
}

func expectEq(t *testing.T, expected, actual, what string) {
	if expected == actual {
		return
	}
	t.Errorf("Unexpected value for %s; got %s (len %d) but expected: %s (len %d)",
		what, escapeString(actual), len(actual), escapeString(expected), len(expected))
}

func TestNameAccessors(t *testing.T) {
	tests := [...][3]string{
		{`form-data; name="foo"`, "foo", ""},
		{` form-data ; name=foo`, "foo", ""},
		{`FORM-DATA;name="foo"`, "foo", ""},
		{` FORM-DATA ; name="foo"`, "foo", ""},
		{` FORM-DATA ; name="foo"`, "foo", ""},
		{` FORM-DATA ; name=foo`, "foo", ""},
		{` FORM-DATA ; filename="foo.txt"; name=foo; baz=quux`, "foo", "foo.txt"},
		{` not-form-data ; filename="bar.txt"; name=foo; baz=quux`, "", "bar.txt"},
	}
	for i, test := range tests {
		p := &mime.SinglePart{Header: make(map[string][]string)}
		p.Header.Set("Content-Disposition", test[0])
		if g, e := p.FormName(), test[1]; g != e {
			t.Errorf("test %d: FormName() = %q; want %q", i, g, e)
		}
		if g, e := p.FileName(), test[2]; g != e {
			t.Errorf("test %d: FileName() = %q; want %q", i, g, e)
		}
	}
}

var longLine = strings.Repeat("\n\n\r\r\r\n\r\000", (1<<20)/8)

func testMultipartBody(sep string) string {
	testBody := `
This is a multi-part message.  This line is ignored.
--MyBoundary
Header1: value1
HEADER2: value2
foo-bar: baz

My value
The end.
--MyBoundary
name: bigsection

[longline]
--MyBoundary
Header1: value1b
HEADER2: value2b
foo-bar: bazb

Line 1
Line 2
Line 3 ends in a newline, but just one.

--MyBoundary

never read data
--MyBoundary--


useless trailer
`
	testBody = strings.Replace(testBody, "\n", sep, -1)
	return strings.Replace(testBody, "[longline]", longLine, 1)
}

func TestMultipart(t *testing.T) {
	bodyReader := strings.NewReader(testMultipartBody("\r\n"))
	testMultipart(t, bodyReader, false)
}

func TestMultipartOnlyNewlines(t *testing.T) {
	bodyReader := strings.NewReader(testMultipartBody("\n"))
	testMultipart(t, bodyReader, true)
}

func TestMultipartSlowInput(t *testing.T) {
	bodyReader := strings.NewReader(testMultipartBody("\r\n"))
	testMultipart(t, &slowReader{bodyReader}, false)
}

func testMultipart(t *testing.T, r io.Reader, onlyNewlines bool) {
	t.Parallel()
	reader := mime.NewMultipartReader(r, "MyBoundary")
	buf := new(bytes.Buffer)

	// Part1
	part, err := reader.NextPart()
	if part == nil || err != nil {
		t.Error("Expected part1")
		return
	}
	if x := part.Header.Get("Header1"); x != "value1" {
		t.Errorf("part.Header.Get(%q) = %q, want %q", "Header1", x, "value1")
	}
	if x := part.Header.Get("foo-bar"); x != "baz" {
		t.Errorf("part.Header.Get(%q) = %q, want %q", "foo-bar", x, "baz")
	}
	if x := part.Header.Get("Foo-Bar"); x != "baz" {
		t.Errorf("part.Header.Get(%q) = %q, want %q", "Foo-Bar", x, "baz")
	}
	buf.Reset()
	if _, err := io.Copy(buf, part); err != nil {
		t.Errorf("part 1 copy: %v", err)
	}

	adjustNewlines := func(s string) string {
		if onlyNewlines {
			return strings.Replace(s, "\r\n", "\n", -1)
		}
		return s
	}

	expectEq(t, adjustNewlines("My value\r\nThe end."), buf.String(), "Value of first part")

	// Part2
	part, err = reader.NextPart()
	if err != nil {
		t.Fatalf("Expected part2; got: %v", err)
		return
	}
	if e, g := "bigsection", part.Header.Get("name"); e != g {
		t.Errorf("part2's name header: expected %q, got %q", e, g)
	}
	buf.Reset()
	if _, err := io.Copy(buf, part); err != nil {
		t.Errorf("part 2 copy: %v", err)
	}
	s := buf.String()
	if len(s) != len(longLine) {
		t.Errorf("part2 body expected long line of length %d; got length %d",
			len(longLine), len(s))
	}
	if s != longLine {
		t.Errorf("part2 long body didn't match")
	}

	// Part3
	part, err = reader.NextPart()
	if part == nil || err != nil {
		t.Error("Expected part3")
		return
	}
	if part.Header.Get("foo-bar") != "bazb" {
		t.Error("Expected foo-bar: bazb")
	}
	buf.Reset()
	if _, err := io.Copy(buf, part); err != nil {
		t.Errorf("part 3 copy: %v", err)
	}
	expectEq(t, adjustNewlines("Line 1\r\nLine 2\r\nLine 3 ends in a newline, but just one.\r\n"),
		buf.String(), "body of part 3")

	// Part4
	part, err = reader.NextPart()
	if part == nil || err != nil {
		t.Error("Expected part 4 without errors")
		return
	}

	// Non-existent part5
	part, err = reader.NextPart()
	if part != nil {
		t.Error("Didn't expect a fifth part.")
	}
	if err != io.EOF {
		t.Errorf("On fifth part expected io.EOF; got %v", err)
	}
}

func TestVariousTextLineEndings(t *testing.T) {
	tests := [...]string{
		"Foo\nBar",
		"Foo\nBar\n",
		"Foo\r\nBar",
		"Foo\r\nBar\r\n",
		"Foo\rBar",
		"Foo\rBar\r",
		"\x00\x01\x02\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10",
	}

	for testNum, expectedBody := range tests {
		body := "--BOUNDARY\r\n" +
			"Content-Disposition: form-data; name=\"value\"\r\n" +
			"\r\n" +
			expectedBody +
			"\r\n--BOUNDARY--\r\n"
		bodyReader := strings.NewReader(body)

		reader := mime.NewMultipartReader(bodyReader, "BOUNDARY")
		buf := new(bytes.Buffer)
		part, err := reader.NextPart()
		if part == nil {
			t.Errorf("Expected a body part on text %d", testNum)
			continue
		}
		if err != nil {
			t.Errorf("Unexpected error on text %d: %v", testNum, err)
			continue
		}
		written, err := io.Copy(buf, part)
		expectEq(t, expectedBody, buf.String(), fmt.Sprintf("test %d", testNum))
		if err != nil {
			t.Errorf("Error copying multipart; bytes=%v, error=%v", written, err)
		}

		part, err = reader.NextPart()
		if part != nil {
			t.Errorf("Unexpected part in test %d", testNum)
		}
		if err != io.EOF {
			t.Errorf("On test %d expected io.EOF; got %v", testNum, err)
		}

	}
}

type maliciousReader struct {
	t *testing.T
	n int
}

const maxReadThreshold = 1 << 20

func (mr *maliciousReader) Read(b []byte) (n int, err error) {
	mr.n += len(b)
	if mr.n >= maxReadThreshold {
		mr.t.Fatal("too much was read")
		return 0, io.EOF
	}
	return len(b), nil
}

func TestLineLimit(t *testing.T) {
	mr := &maliciousReader{t: t}
	r := mime.NewMultipartReader(mr, "fooBoundary")
	part, err := r.NextPart()
	if part != nil {
		t.Errorf("unexpected part read")
	}
	if err == nil {
		t.Errorf("expected an error")
	}
	if mr.n >= maxReadThreshold {
		t.Errorf("expected to read < %d bytes; read %d", maxReadThreshold, mr.n)
	}
}

func TestMultipartTruncated(t *testing.T) {
	testBody := `
This is a multi-part message.  This line is ignored.
--MyBoundary
foo-bar: baz

Oh no, premature EOF!
`
	body := strings.Replace(testBody, "\n", "\r\n", -1)
	bodyReader := strings.NewReader(body)
	r := mime.NewMultipartReader(bodyReader, "MyBoundary")

	part, err := r.NextPart()
	if err != nil {
		t.Fatalf("didn't get a part")
	}
	_, err = io.Copy(ioutil.Discard, part)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected error io.ErrUnexpectedEOF; got %v", err)
	}
}

type slowReader struct {
	r io.Reader
}

func (s *slowReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return s.r.Read(p)
	}
	return s.r.Read(p[:1])
}

type sentinelReader struct {
	// done is closed when this reader is read from.
	done chan struct{}
}

func (s *sentinelReader) Read([]byte) (int, error) {
	if s.done != nil {
		close(s.done)
		s.done = nil
	}
	return 0, io.EOF
}

// TestMultipartStreamReadahead tests that PartReader does not block
// on reading past the end of a part, ensuring that it can be used on
// a stream like multipart/x-mixed-replace. See golang.org/issue/15431
func TestMultipartStreamReadahead(t *testing.T) {
	testBody1 := `
This is a multi-part message.  This line is ignored.
--MyBoundary
foo-bar: baz

Body
--MyBoundary
`
	testBody2 := `foo-bar: bop

Body 2
--MyBoundary--
`
	done1 := make(chan struct{})
	reader := mime.NewMultipartReader(
		io.MultiReader(
			strings.NewReader(testBody1),
			&sentinelReader{done1},
			strings.NewReader(testBody2)),
		"MyBoundary")

	var i int
	readPart := func(hdr hdr.Header, body string) {
		part, err := reader.NextPart()
		if part == nil || err != nil {
			t.Fatalf("Part %d: NextPart failed: %v", i, err)
		}

		if !reflect.DeepEqual(part.Header, hdr) {
			t.Errorf("Part %d: part.Header = %v, want %v", i, part.Header, hdr)
		}
		data, err := ioutil.ReadAll(part)
		expectEq(t, body, string(data), fmt.Sprintf("Part %d body", i))
		if err != nil {
			t.Fatalf("Part %d: ReadAll failed: %v", i, err)
		}
		i++
	}

	readPart(hdr.Header{"Foo-Bar": {"baz"}}, "Body")

	select {
	case <-done1:
		t.Errorf("Reader read past second boundary")
	default:
	}

	readPart(hdr.Header{"Foo-Bar": {"bop"}}, "Body 2")
}

func TestLineContinuation(t *testing.T) {
	// This body, extracted from an email, contains headers that span multiple
	// lines.

	// TODO: The original mail ended with a double-newline before the
	// final delimiter; this was manually edited to use a CRLF.
	testBody :=
		"\n--Apple-Mail-2-292336769\nContent-Transfer-Encoding: 7bit\nContent-Type: text/plain;\n\tcharset=US-ASCII;\n\tdelsp=yes;\n\tformat=flowed\n\nI'm finding the same thing happening on my system (10.4.1).\n\n\n--Apple-Mail-2-292336769\nContent-Transfer-Encoding: quoted-printable\nContent-Type: text/html;\n\tcharset=ISO-8859-1\n\n<HTML><BODY>I'm finding the same thing =\nhappening on my system (10.4.1).=A0 But I built it with XCode =\n2.0.</BODY></=\nHTML>=\n\r\n--Apple-Mail-2-292336769--\n"

	r := mime.NewMultipartReader(strings.NewReader(testBody), "Apple-Mail-2-292336769")

	for i := 0; i < 2; i++ {
		part, err := r.NextPart()
		if err != nil {
			t.Fatalf("didn't get a part")
		}
		var buf bytes.Buffer
		n, err := io.Copy(&buf, part)
		if err != nil {
			t.Errorf("error reading part: %v\nread so far: %q", err, buf.String())
		}
		if n <= 0 {
			t.Errorf("read %d bytes; expected >0", n)
		}
	}
}

func TestQuotedPrintableEncoding(t *testing.T) {
	// From https://golang.org/issue/4411
	body := "--0016e68ee29c5d515f04cedf6733\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=text\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nwords words words words words words words words words words words words wor=\r\nds words words words words words words words words words words words words =\r\nwords words words words words words words words words words words words wor=\r\nds words words words words words words words words words words words words =\r\nwords words words words words words words words words\r\n--0016e68ee29c5d515f04cedf6733\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=submit\r\n\r\nSubmit\r\n--0016e68ee29c5d515f04cedf6733--"
	r := mime.NewMultipartReader(strings.NewReader(body), "0016e68ee29c5d515f04cedf6733")
	part, err := r.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if te, ok := part.Header["Content-Transfer-Encoding"]; ok {
		t.Errorf("unexpected Content-Transfer-Encoding of %q", te)
	}
	var buf bytes.Buffer
	_, err = io.Copy(&buf, part)
	if err != nil {
		t.Error(err)
	}
	got := buf.String()
	want := "words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words words"
	if got != want {
		t.Errorf("wrong part value:\n got: %q\nwant: %q", got, want)
	}
}

// Test parsing an image attachment from gmail, which previously failed.
func TestNested(t *testing.T) {
	// nested-mime is the body part of a multipart/mixed email
	// with boundary e89a8ff1c1e83553e304be640612
	f, err := os.Open("testdata/nested-mime")
	if err != nil {
		t.Skip("Skipping : probably missing files")
	}
	defer f.Close()
	mr := mime.NewMultipartReader(f, "e89a8ff1c1e83553e304be640612")
	p, err := mr.NextPart()
	if err != nil {
		t.Fatalf("error reading first section (alternative): %v", err)
	}

	// Read the inner text/plain and text/html sections of the multipart/alternative.
	mr2 := mime.NewMultipartReader(p, "e89a8ff1c1e83553e004be640610")
	p, err = mr2.NextPart()
	if err != nil {
		t.Fatalf("reading text/plain part: %v", err)
	}
	if b, err := ioutil.ReadAll(p); string(b) != "*body*\r\n" || err != nil {
		t.Fatalf("reading text/plain part: got %q, %v", b, err)
	}
	p, err = mr2.NextPart()
	if err != nil {
		t.Fatalf("reading text/html part: %v", err)
	}
	if b, err := ioutil.ReadAll(p); string(b) != "<b>body</b>\r\n" || err != nil {
		t.Fatalf("reading text/html part: got %q, %v", b, err)
	}

	p, err = mr2.NextPart()
	if err != io.EOF {
		t.Fatalf("final inner NextPart = %v; want io.EOF", err)
	}

	// Back to the outer multipart/mixed, reading the image attachment.
	_, err = mr.NextPart()
	if err != nil {
		t.Fatalf("error reading the image attachment at the end: %v", err)
	}

	_, err = mr.NextPart()
	if err != io.EOF {
		t.Fatalf("final outer NextPart = %v; want io.EOF", err)
	}
}

type headerBody struct {
	header hdr.Header
	body   string
}

func formData(key, value string) headerBody {
	return headerBody{
		hdr.Header{
			"Content-Type":        {"text/plain; charset=ISO-8859-1"},
			"Content-Disposition": {"form-data; name=" + key},
		},
		value,
	}
}

type parseTest struct {
	name    string
	in, sep string
	want    []headerBody
}

var parseTests = []parseTest{
	// Actual body from App Engine on a blob upload. The final part (the
	// Content-Type: message/external-body) is what App Engine replaces
	// the uploaded file with. The other form fields (prefixed with
	// "other" in their form-data name) are unchanged. A bug was
	// reported with blob uploads failing when the other fields were
	// empty. This was the MIME POST body that previously failed.
	{
		name: "App Engine post",
		sep:  "00151757727e9583fd04bfbca4c6",
		in:   "--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=otherEmpty1\r\n\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=otherFoo1\r\n\r\nfoo\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=otherFoo2\r\n\r\nfoo\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=otherEmpty2\r\n\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=otherRepeatFoo\r\n\r\nfoo\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=otherRepeatFoo\r\n\r\nfoo\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=otherRepeatEmpty\r\n\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=otherRepeatEmpty\r\n\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Disposition: form-data; name=submit\r\n\r\nSubmit\r\n--00151757727e9583fd04bfbca4c6\r\nContent-Type: message/external-body; charset=ISO-8859-1; blob-key=AHAZQqG84qllx7HUqO_oou5EvdYQNS3Mbbkb0RjjBoM_Kc1UqEN2ygDxWiyCPulIhpHRPx-VbpB6RX4MrsqhWAi_ZxJ48O9P2cTIACbvATHvg7IgbvZytyGMpL7xO1tlIvgwcM47JNfv_tGhy1XwyEUO8oldjPqg5Q\r\nContent-Disposition: form-data; name=file; filename=\"fall.png\"\r\n\r\nContent-Type: image/png\r\nContent-Length: 232303\r\nX-AppEngine-Upload-Creation: 2012-05-10 23:14:02.715173\r\nContent-MD5: MzRjODU1ZDZhZGU1NmRlOWEwZmMwMDdlODBmZTA0NzA=\r\nContent-Disposition: form-data; name=file; filename=\"fall.png\"\r\n\r\n\r\n--00151757727e9583fd04bfbca4c6--",
		want: []headerBody{
			formData("otherEmpty1", ""),
			formData("otherFoo1", "foo"),
			formData("otherFoo2", "foo"),
			formData("otherEmpty2", ""),
			formData("otherRepeatFoo", "foo"),
			formData("otherRepeatFoo", "foo"),
			formData("otherRepeatEmpty", ""),
			formData("otherRepeatEmpty", ""),
			formData("submit", "Submit"),
			{hdr.Header{
				"Content-Type":        {"message/external-body; charset=ISO-8859-1; blob-key=AHAZQqG84qllx7HUqO_oou5EvdYQNS3Mbbkb0RjjBoM_Kc1UqEN2ygDxWiyCPulIhpHRPx-VbpB6RX4MrsqhWAi_ZxJ48O9P2cTIACbvATHvg7IgbvZytyGMpL7xO1tlIvgwcM47JNfv_tGhy1XwyEUO8oldjPqg5Q"},
				"Content-Disposition": {"form-data; name=file; filename=\"fall.png\""},
			}, "Content-Type: image/png\r\nContent-Length: 232303\r\nX-AppEngine-Upload-Creation: 2012-05-10 23:14:02.715173\r\nContent-MD5: MzRjODU1ZDZhZGU1NmRlOWEwZmMwMDdlODBmZTA0NzA=\r\nContent-Disposition: form-data; name=file; filename=\"fall.png\"\r\n\r\n"},
		},
	},

	// Single empty part, ended with --boundary immediately after headers.
	{
		name: "single empty part, --boundary",
		sep:  "abc",
		in:   "--abc\r\nFoo: bar\r\n\r\n--abc--",
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, ""},
		},
	},

	// Single empty part, ended with \r\n--boundary immediately after headers.
	{
		name: "single empty part, \r\n--boundary",
		sep:  "abc",
		in:   "--abc\r\nFoo: bar\r\n\r\n\r\n--abc--",
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, ""},
		},
	},

	// Final part empty.
	{
		name: "final part empty",
		sep:  "abc",
		in:   "--abc\r\nFoo: bar\r\n\r\n--abc\r\nFoo2: bar2\r\n\r\n--abc--",
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, ""},
			{hdr.Header{"Foo2": {"bar2"}}, ""},
		},
	},

	// Final part empty with newlines after final separator.
	{
		name: "final part empty then crlf",
		sep:  "abc",
		in:   "--abc\r\nFoo: bar\r\n\r\n--abc--\r\n",
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, ""},
		},
	},

	// Final part empty with lwsp-chars after final separator.
	{
		name: "final part empty then lwsp",
		sep:  "abc",
		in:   "--abc\r\nFoo: bar\r\n\r\n--abc-- \t",
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, ""},
		},
	},

	// No parts (empty form as submitted by Chrome)
	{
		name: "no parts",
		sep:  "----WebKitFormBoundaryQfEAfzFOiSemeHfA",
		in:   "------WebKitFormBoundaryQfEAfzFOiSemeHfA--\r\n",
		want: []headerBody{},
	},

	// Part containing data starting with the boundary, but with additional suffix.
	{
		name: "fake separator as data",
		sep:  "sep",
		in:   "--sep\r\nFoo: bar\r\n\r\n--sepFAKE\r\n--sep--",
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, "--sepFAKE"},
		},
	},

	// Part containing a boundary with whitespace following it.
	{
		name: "boundary with whitespace",
		sep:  "sep",
		in:   "--sep \r\nFoo: bar\r\n\r\ntext\r\n--sep--",
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, "text"},
		},
	},

	// With ignored leading line.
	{
		name: "leading line",
		sep:  "MyBoundary",
		in: strings.Replace(`This is a multi-part message.  This line is ignored.
--MyBoundary
foo: bar


--MyBoundary--`, "\n", "\r\n", -1),
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, ""},
		},
	},

	// Issue 10616; minimal
	{
		name: "issue 10616 minimal",
		sep:  "sep",
		in: "--sep \r\nFoo: bar\r\n\r\n" +
			"a\r\n" +
			"--sep_alt\r\n" +
			"b\r\n" +
			"\r\n--sep--",
		want: []headerBody{
			{hdr.Header{"Foo": {"bar"}}, "a\r\n--sep_alt\r\nb\r\n"},
		},
	},

	// Issue 10616; full example from bug.
	{
		name: "nested separator prefix is outer separator",
		sep:  "----=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9",
		in: strings.Replace(`------=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9
Content-Type: multipart/alternative; boundary="----=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9_alt"

------=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9_alt
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: 8bit

This is a multi-part message in MIME format.

------=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9_alt
Content-Type: text/html; charset="utf-8"
Content-Transfer-Encoding: 8bit

html things
------=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9_alt--
------=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9--`, "\n", "\r\n", -1),
		want: []headerBody{
			{hdr.Header{"Content-Type": {`multipart/alternative; boundary="----=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9_alt"`}},
				strings.Replace(`------=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9_alt
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: 8bit

This is a multi-part message in MIME format.

------=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9_alt
Content-Type: text/html; charset="utf-8"
Content-Transfer-Encoding: 8bit

html things
------=_NextPart_4c2fbafd7ec4c8bf08034fe724b608d9_alt--`, "\n", "\r\n", -1),
			},
		},
	},
	// Issue 12662: Check that we don't consume the leading \r if the peekBuffer
	// ends in '\r\n--separator-'
	{
		name: "peek buffer boundary condition",
		sep:  "00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db",
		in: strings.Replace(`--00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db
Content-Disposition: form-data; name="block"; filename="block"
Content-Type: application/octet-stream

`+strings.Repeat("A", peekBufferSize-65)+"\n--00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db--", "\n", "\r\n", -1),
		want: []headerBody{
			{hdr.Header{"Content-Type": {`application/octet-stream`}, "Content-Disposition": {`form-data; name="block"; filename="block"`}},
				strings.Repeat("A", peekBufferSize-65),
			},
		},
	},
	// Issue 12662: Same test as above with \r\n at the end
	{
		name: "peek buffer boundary condition",
		sep:  "00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db",
		in: strings.Replace(`--00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db
Content-Disposition: form-data; name="block"; filename="block"
Content-Type: application/octet-stream

`+strings.Repeat("A", peekBufferSize-65)+"\n--00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db--\n", "\n", "\r\n", -1),
		want: []headerBody{
			{hdr.Header{"Content-Type": {`application/octet-stream`}, "Content-Disposition": {`form-data; name="block"; filename="block"`}},
				strings.Repeat("A", peekBufferSize-65),
			},
		},
	},
	// Issue 12662v2: We want to make sure that for short buffers that end with
	// '\r\n--separator-' we always consume at least one (valid) symbol from the
	// peekBuffer
	{
		name: "peek buffer boundary condition",
		sep:  "aaaaaaaaaa00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db",
		in: strings.Replace(`--aaaaaaaaaa00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db
Content-Disposition: form-data; name="block"; filename="block"
Content-Type: application/octet-stream

`+strings.Repeat("A", peekBufferSize)+"\n--aaaaaaaaaa00ffded004d4dd0fdf945fbdef9d9050cfd6a13a821846299b27fc71b9db--", "\n", "\r\n", -1),
		want: []headerBody{
			{hdr.Header{"Content-Type": {`application/octet-stream`}, "Content-Disposition": {`form-data; name="block"; filename="block"`}},
				strings.Repeat("A", peekBufferSize),
			},
		},
	},
	// Context: https://github.com/camlistore/camlistore/issues/642
	// If the file contents in the form happens to have a size such as:
	// size = peekBufferSize - (len("\n--") + len(boundary) + len("\r") + 1), (modulo peekBufferSize)
	// then peekBufferSeparatorIndex was wrongly returning (-1, false), which was leading to an nCopy
	// cut such as:
	// "somedata\r| |\n--Boundary\r" (instead of "somedata| |\r\n--Boundary\r"), which was making the
	// subsequent Read miss the boundary.
	{
		name: "safeCount off by one",
		sep:  "08b84578eabc563dcba967a945cdf0d9f613864a8f4a716f0e81caa71a74",
		in: strings.Replace(`--08b84578eabc563dcba967a945cdf0d9f613864a8f4a716f0e81caa71a74
Content-Disposition: form-data; name="myfile"; filename="my-file.txt"
Content-Type: application/octet-stream

`, "\n", "\r\n", -1) +
			strings.Repeat("A", peekBufferSize-(len("\n--")+len("08b84578eabc563dcba967a945cdf0d9f613864a8f4a716f0e81caa71a74")+len("\r")+1)) +
			strings.Replace(`
--08b84578eabc563dcba967a945cdf0d9f613864a8f4a716f0e81caa71a74
Content-Disposition: form-data; name="key"

val
--08b84578eabc563dcba967a945cdf0d9f613864a8f4a716f0e81caa71a74--
`, "\n", "\r\n", -1),
		want: []headerBody{
			{hdr.Header{"Content-Type": {`application/octet-stream`}, "Content-Disposition": {`form-data; name="myfile"; filename="my-file.txt"`}},
				strings.Repeat("A", peekBufferSize-(len("\n--")+len("08b84578eabc563dcba967a945cdf0d9f613864a8f4a716f0e81caa71a74")+len("\r")+1)),
			},
			{hdr.Header{"Content-Disposition": {`form-data; name="key"`}},
				"val",
			},
		},
	},

	roundTripParseTest(),
}

func TestParse(t *testing.T) {
Cases:
	for _, tt := range parseTests {
		r := mime.NewMultipartReader(strings.NewReader(tt.in), tt.sep)
		got := []headerBody{}
		for {
			p, err := r.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("in test %q, NextPart: %v", tt.name, err)
				continue Cases
			}
			pbody, err := ioutil.ReadAll(p)
			if err != nil {
				t.Errorf("in test %q, error reading part: %v", tt.name, err)
				continue Cases
			}
			got = append(got, headerBody{p.Header, string(pbody)})
		}
		if !reflect.DeepEqual(tt.want, got) {
			t.Errorf("test %q:\n got: %v\nwant: %v", tt.name, got, tt.want)
			if len(tt.want) != len(got) {
				t.Errorf("test %q: got %d parts, want %d", tt.name, len(got), len(tt.want))
			} else if len(got) > 1 {
				for pi, wantPart := range tt.want {
					if !reflect.DeepEqual(wantPart, got[pi]) {
						t.Errorf("test %q, part %d:\n got: %v\nwant: %v", tt.name, pi, got[pi], wantPart)
					}
				}
			}
		}
	}
}

func partsFromReader(r *mime.MultipartReader) ([]headerBody, error) {
	got := []headerBody{}
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return got, nil
		}
		if err != nil {
			return nil, fmt.Errorf("NextPart: %v", err)
		}
		pbody, err := ioutil.ReadAll(p)
		if err != nil {
			return nil, fmt.Errorf("error reading part: %v", err)
		}
		got = append(got, headerBody{p.Header, string(pbody)})
	}
}

func TestParseAllSizes(t *testing.T) {
	t.Parallel()
	const maxSize = 5 << 10
	var buf bytes.Buffer
	body := strings.Repeat("a", maxSize)
	bodyb := []byte(body)
	for size := 0; size < maxSize; size++ {
		buf.Reset()
		w := mime.NewMultipartWriter(&buf)
		part, _ := w.CreateFormField("f")
		part.Write(bodyb[:size])
		part, _ = w.CreateFormField("key")
		part.Write([]byte("val"))
		w.Close()
		r := mime.NewMultipartReader(&buf, w.Boundary())
		got, err := partsFromReader(r)
		if err != nil {
			t.Errorf("For size %d: %v", size, err)
			continue
		}
		if len(got) != 2 {
			t.Errorf("For size %d, num parts = %d; want 2", size, len(got))
			continue
		}
		if got[0].body != body[:size] {
			t.Errorf("For size %d, got unexpected len %d: %q", size, len(got[0].body), got[0].body)
		}
	}
}

func roundTripParseTest() parseTest {
	t := parseTest{
		name: "round trip",
		want: []headerBody{
			formData("empty", ""),
			formData("lf", "\n"),
			formData("cr", "\r"),
			formData("crlf", "\r\n"),
			formData("foo", "bar"),
		},
	}
	var buf bytes.Buffer
	w := mime.NewMultipartWriter(&buf)
	for _, p := range t.want {
		pw, err := w.CreatePart(p.header)
		if err != nil {
			panic(err)
		}
		_, err = pw.Write([]byte(p.body))
		if err != nil {
			panic(err)
		}
	}
	w.Close()
	t.in = buf.String()
	t.sep = w.Boundary()
	return t
}

func TestReadForm(t *testing.T) {
	b := strings.NewReader(strings.Replace(message, "\n", "\r\n", -1))
	r := mime.NewMultipartReader(b, boundary)
	f, err := r.ReadForm(25)
	if err != nil {
		t.Fatal("ReadForm:", err)
	}
	defer f.RemoveAll()
	if g, e := f.Value["texta"][0], textaValue; g != e {
		t.Errorf("texta value = %q, want %q", g, e)
	}
	if g, e := f.Value["textb"][0], textbValue; g != e {
		t.Errorf("texta value = %q, want %q", g, e)
	}
	fd := testFiles(t, f.File["filea"][0], "filea.txt", fileaContents)
	if _, ok := fd.(*os.File); ok {
		t.Error("file is *os.File, should not be")
	}
	fd.Close()
	fd = testFiles(t, f.File["fileb"][0], "fileb.txt", filebContents)
	if _, ok := fd.(*os.File); !ok {
		t.Errorf("file has unexpected underlying type %T", fd)
	}
	fd.Close()
}

/**
func TestReadFormWithNamelessFile(t *testing.T) {
	b := strings.NewReader(strings.Replace(messageWithFileWithoutName, "\n", "\r\n", -1))
	r := mime.NewMultipartReader(b, boundary)
	f, err := r.ReadForm(25)
	if err != nil {
		t.Fatal("ReadForm:", err)
	}
	defer f.RemoveAll()

	fd := testFiles(t, f.File["hiddenfile"][0], "", filebContents)
	type sectionReadCloser struct {
		*io.SectionReader
	}
	if _, ok := fd.(sectionReadCloser); !ok {
		t.Errorf("file has unexpected underlying type %T", fd)
	}
	fd.Close()

}
**/
func testFiles(t *testing.T, fh *mime.FileHeader, efn, econtent string) mime.File {
	if fh.Filename != efn {
		t.Errorf("filename = %q, want %q", fh.Filename, efn)
	}
	if fh.Size != int64(len(econtent)) {
		t.Errorf("size = %d, want %d", fh.Size, len(econtent))
	}
	f, err := fh.Open()
	if err != nil {
		t.Fatal("opening file:", err)
	}
	b := new(bytes.Buffer)
	_, err = io.Copy(b, f)
	if err != nil {
		t.Fatal("copying contents:", err)
	}
	if g := b.String(); g != econtent {
		t.Errorf("contents = %q, want %q", g, econtent)
	}
	return f
}

const (
	fileaContents = "This is a test file."
	filebContents = "Another test file."
	textaValue    = "foo"
	textbValue    = "bar"
	boundary      = `MyBoundary`
)

const messageWithFileWithoutName = `
--MyBoundary
Content-Disposition: form-data; name="hiddenfile"; filename=""
Content-Type: text/plain

` + filebContents + `
--MyBoundary--
`

const message = `
--MyBoundary
Content-Disposition: form-data; name="filea"; filename="filea.txt"
Content-Type: text/plain

` + fileaContents + `
--MyBoundary
Content-Disposition: form-data; name="fileb"; filename="fileb.txt"
Content-Type: text/plain

` + filebContents + `
--MyBoundary
Content-Disposition: form-data; name="texta"

` + textaValue + `
--MyBoundary
Content-Disposition: form-data; name="textb"

` + textbValue + `
--MyBoundary--
`

func TestReadForm_NoReadAfterEOF(t *testing.T) {
	maxMemory := int64(32) << 20
	boundary := `---------------------------8d345eef0d38dc9`
	body := `
-----------------------------8d345eef0d38dc9
Content-Disposition: form-data; name="version"

171
-----------------------------8d345eef0d38dc9--`

	mr := mime.NewMultipartReader(&failOnReadAfterErrorReader{t: t, r: strings.NewReader(body)}, boundary)

	f, err := mr.ReadForm(maxMemory)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Got: %#v", f)
}

// failOnReadAfterErrorReader is an io.Reader wrapping r.
// It fails t if any Read is called after a failing Read.
type failOnReadAfterErrorReader struct {
	t      *testing.T
	r      io.Reader
	sawErr error
}

func (r *failOnReadAfterErrorReader) Read(p []byte) (n int, err error) {
	if r.sawErr != nil {
		r.t.Fatalf("unexpected Read on Reader after previous read saw error %v", r.sawErr)
	}
	n, err = r.r.Read(p)
	r.sawErr = err
	return
}

// TestReadForm_NonFileMaxMemory asserts that the ReadForm maxMemory limit is applied
// while processing non-file form data as well as file form data.
func TestReadForm_NonFileMaxMemory(t *testing.T) {
	largeTextValue := strings.Repeat("1", (10<<20)+25)
	message := `--MyBoundary
Content-Disposition: form-data; name="largetext"

` + largeTextValue + `
--MyBoundary--
`

	testBody := strings.Replace(message, "\n", "\r\n", -1)
	testCases := []struct {
		name      string
		maxMemory int64
		err       error
	}{
		{"smaller", 50, nil},
		{"exact-fit", 25, nil},
		//{"too-large", 0, ErrMessageTooLarge},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			b := strings.NewReader(testBody)
			r := mime.NewMultipartReader(b, boundary)
			f, err := r.ReadForm(tc.maxMemory)
			if err == nil {
				defer f.RemoveAll()
			}
			if tc.err != err {
				t.Fatalf("ReadForm error - got: %v; expected: %v", tc.err, err)
			}
			if err == nil {
				if g := f.Value["largetext"][0]; g != largeTextValue {
					t.Errorf("largetext mismatch: got size: %v, expected size: %v", len(g), len(largeTextValue))
				}
			}
		})
	}
}
