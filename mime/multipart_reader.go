/*
 * Copyright (c) 2018 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
 */

package mime

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	. "github.com/badu/http/hdr"
)

// ReadForm parses an entire multipart message whose parts have
// a Content-Disposition of "form-data".
// It stores up to maxMemory bytes + 10MB (reserved for non-file parts)
// in memory. File parts which can't be stored in memory will be stored on
// disk in temporary files.
// It returns ErrMessageTooLarge if all non-file parts can't be stored in
// memory.
func (r *MultipartReader) ReadForm(maxMemory int64) (*Form, error) {
	return r.readForm(maxMemory)
}

func (r *MultipartReader) readForm(maxMemory int64) (_ *Form, err error) {
	form := &Form{make(map[string][]string), make(map[string][]*FileHeader)}
	defer func() {
		if err != nil {
			form.RemoveAll()
		}
	}()

	// Reserve an additional 10 MB for non-file parts.
	maxValueBytes := maxMemory + int64(10<<20)
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		name := p.FormName()
		if name == "" {
			continue
		}
		filename := p.FileName()

		var b bytes.Buffer

		_, hasContentTypeHeader := p.Header[ContentType]
		if !hasContentTypeHeader && filename == "" {
			// value, store as string in memory
			n, err := io.CopyN(&b, p, maxValueBytes+1)
			if err != nil && err != io.EOF {
				return nil, err
			}
			maxValueBytes -= n
			if maxValueBytes < 0 {
				return nil, ErrMessageTooLarge
			}
			form.Value[name] = append(form.Value[name], b.String())
			continue
		}

		// file, store in memory or on disk
		fh := &FileHeader{
			Filename: filename,
			Header:   p.Header,
		}
		n, err := io.CopyN(&b, p, maxMemory+1)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n > maxMemory {
			// too big, write to disk and flush buffer
			file, err := ioutil.TempFile("", "multipart-")
			if err != nil {
				return nil, err
			}
			size, err := io.Copy(file, io.MultiReader(&b, p))
			if cerr := file.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				os.Remove(file.Name())
				return nil, err
			}
			fh.tmpfile = file.Name()
			fh.Size = size
		} else {
			fh.content = b.Bytes()
			fh.Size = int64(len(fh.content))
			maxMemory -= n
			maxValueBytes -= n
		}
		form.File[name] = append(form.File[name], fh)
	}

	return form, nil
}

// NextPart returns the next part in the multipart or an error.
// When there are no more parts, the error io.EOF is returned.
func (r *MultipartReader) NextPart() (*SinglePart, error) {
	if r.currentPart != nil {
		r.currentPart.Close()
	}

	expectNewPart := false
	for {
		line, err := r.bufReader.ReadSlice('\n')

		if err == io.EOF && r.isFinalBoundary(line) {
			// If the buffer ends in "--boundary--" without the
			// trailing "\r\n", ReadSlice will return an error
			// (since it's missing the '\n'), but this is a valid
			// multipart EOF so we need to return io.EOF instead of
			// a fmt-wrapped one.
			return nil, io.EOF
		}
		if err != nil {
			return nil, fmt.Errorf("multipart: NextPart: %v", err)
		}

		if r.IsBoundaryDelimiterLine(line) {
			r.partsRead++
			bp, err := newPart(r)
			if err != nil {
				return nil, err
			}
			r.currentPart = bp
			return bp, nil
		}

		if r.isFinalBoundary(line) {
			// Expected EOF
			return nil, io.EOF
		}

		if expectNewPart {
			return nil, fmt.Errorf("multipart: expecting a new Part; got line %q", string(line))
		}

		if r.partsRead == 0 {
			// skip line
			continue
		}

		// Consume the "\n" or "\r\n" separator between the
		// body of the previous part and the boundary line we
		// now expect will follow. (either a new part or the
		// end boundary)
		if bytes.Equal(line, r.newLine) {
			expectNewPart = true
			continue
		}

		return nil, fmt.Errorf("multipart: unexpected line in Next(): %q", line)
	}
}

// isFinalBoundary reports whether line is the final boundary line
// indicating that all parts are over.
// It matches `^--boundary--[ \t]*(\r\n)?$`
//len(s) < len(prefix) || !bytes.Equal(s[0:len(prefix)], prefix)
func (r *MultipartReader) isFinalBoundary(line []byte) bool {
	//@comment : was `if !bytes.HasPrefix(line, reader.dashBoundaryDash) {`
	if len(line) < len(r.dashBoundaryDash) || !bytes.Equal(line[0:len(r.dashBoundaryDash)], r.dashBoundaryDash) {
		return false
	}
	rest := line[len(r.dashBoundaryDash):]
	rest = skipLWSPChar(rest)
	return len(rest) == 0 || bytes.Equal(rest, r.newLine)
}

func (r *MultipartReader) IsBoundaryDelimiterLine(line []byte) (ret bool) {
	// http://tools.ietf.org/html/rfc2046#section-5.1
	//   The boundary delimiter line is then defined as a line
	//   consisting entirely of two hyphen characters ("-",
	//   decimal value 45) followed by the boundary parameter
	//   value from the Content-Type header field, optional linear
	//   whitespace, and a terminating CRLF.
	//@comment : was `if !bytes.HasPrefix(line, reader.dashBoundary) {`
	if len(line) < len(r.dashBoundary) || !bytes.Equal(line[0:len(r.dashBoundary)], r.dashBoundary) {
		return false
	}
	rest := line[len(r.dashBoundary):]
	rest = skipLWSPChar(rest)

	// On the first part, see our lines are ending in \n instead of \r\n
	// and switch into that mode if so. This is a violation of the spec,
	// but occurs in practice.
	if r.partsRead == 0 && len(rest) == 1 && rest[0] == '\n' {
		r.newLine = r.newLine[1:]
		r.nlDashBoundary = r.nlDashBoundary[1:]
	}
	return bytes.Equal(rest, r.newLine)
}
