package mime

import (
	"bytes"
	"io"
	"os"
)

// Open opens and returns the FileHeader's associated File.
func (fh *FileHeader) Open() (File, error) {
	if b := fh.content; b != nil {
		r := io.NewSectionReader(bytes.NewReader(b), 0, int64(len(b)))
		return sectionReadCloser{r}, nil
	}
	return os.Open(fh.tmpfile)
}
