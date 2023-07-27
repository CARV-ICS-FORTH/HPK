// Copyright © 2023 FORTH-ICS
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// The code is adapted from Podman 2023.

package container

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	// LogTimeFormat is the time format used in the log.
	// It is a modified version of RFC3339Nano that guarantees trailing
	// zeroes are not trimmed, taken from
	// https://github.com/golang/go/issues/19635
	LogTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

	// PartialLogType signifies a log line that exceeded the buffer
	// length and needed to spill into a new line
	PartialLogType = "P"

	// FullLogType signifies a log line is full
	FullLogType = "F"

	// ANSIEscapeResetCode is a code that resets all colors and text effects
	ANSIEscapeResetCode = "\033[0m"
)

// LogOptions is the options you can use for logs
type LogOptions struct {
	Details    bool
	Follow     bool
	Since      time.Time
	Until      time.Time
	Tail       int64
	Timestamps bool
	Colors     bool
	Multi      bool
	WaitGroup  *sync.WaitGroup
	UseName    bool
}

// LogLine describes the information for each line of a log
type LogLine struct {
	Device       string
	ParseLogType string
	Time         time.Time
	Msg          string
	CID          string
	CName        string
	ColorID      int64
}

func GetTailLog(path string, tail int) ([]string, error) {
	var (
		nllCounter int
		leftover   string
		tailLog    []string
	)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rr, err := NewReverseReader(f)
	if err != nil {
		return nil, err
	}

	inputs := make(chan []string)
	go func() {
		for {
			s, err := rr.Read()
			if err != nil {
				if errors.Is(err, io.EOF) {
					inputs <- []string{leftover}
				} else {
					logrus.Error(err)
				}
				close(inputs)
				if err := f.Close(); err != nil {
					logrus.Error(err)
				}
				break
			}
			line := strings.Split(s+leftover, "\n")
			if len(line) > 1 {
				inputs <- line[1:]
			}
			leftover = line[0]
		}
	}()

	for i := range inputs {
		// the incoming array is FIFO; we want FIFO so
		// reverse the slice read order
		for j := len(i) - 1; j >= 0; j-- {
			// lines that are "" are junk
			if len(i[j]) < 1 {
				continue
			}
			// read the content in reverse and add each nll until we have the same
			// number of F type messages as the desired tail
			tailLog = append(tailLog, i[j])
			nllCounter++
		}
		// if we have enough log lines, we can hang up
		if nllCounter >= tail {
			break
		}
	}

	return tailLog, nil
}

// getColor returns an ANSI escape code for color based on the colorID
func getColor(colorID int64) string {
	colors := map[int64]string{
		0: "\033[37m", // Light Gray
		1: "\033[31m", // Red
		2: "\033[33m", // Yellow
		3: "\033[34m", // Blue
		4: "\033[35m", // Magenta
		5: "\033[36m", // Cyan
		6: "\033[32m", // Green
	}
	return colors[colorID%int64(len(colors))]
}

func (l *LogLine) colorize(prefix string) string {
	return getColor(l.ColorID) + prefix + l.Msg + ANSIEscapeResetCode
}

// String converts a log line to a string for output given whether a detail
// bool is specified.
func (l *LogLine) String(options *LogOptions) string {
	var out string
	if options.Multi {
		if options.UseName {
			out = l.CName + " "
		} else {
			cid := l.CID
			if len(cid) > 12 {
				cid = cid[:12]
			}
			out = fmt.Sprintf("%s ", cid)
		}
	}

	if options.Timestamps {
		out += fmt.Sprintf("%s ", l.Time.Format(LogTimeFormat))
	}

	if options.Colors {
		out = l.colorize(out)
	} else {
		out += l.Msg
	}

	return out
}

// Since returns a bool as to whether a log line occurred after a given time
func (l *LogLine) Since(since time.Time) bool {
	return l.Time.After(since) || since.IsZero()
}

// Until returns a bool as to whether a log line occurred before a given time
func (l *LogLine) Until(until time.Time) bool {
	return l.Time.Before(until) || until.IsZero()
}

// NewLogLine creates a logLine struct from a container log string
func NewLogLine(line string) (*LogLine, error) {
	splitLine := strings.Split(line, " ")
	if len(splitLine) < 4 {
		return nil, fmt.Errorf("'%s' is not a valid container log line", line)
	}
	logTime, err := time.Parse(LogTimeFormat, splitLine[0])
	if err != nil {
		return nil, fmt.Errorf("unable to convert time %s from container log: %w", splitLine[0], err)
	}
	l := LogLine{
		Time:         logTime,
		Device:       splitLine[1],
		ParseLogType: splitLine[2],
		Msg:          strings.Join(splitLine[3:], " "),
	}
	return &l, nil
}

// Partial returns a bool if the log line is a partial log type
func (l *LogLine) Partial() bool {
	return l.ParseLogType == PartialLogType
}

func (l *LogLine) Write(stdout io.Writer, stderr io.Writer, logOpts *LogOptions) {
	switch l.Device {
	case "stdout":
		if stdout != nil {
			if l.Partial() {
				fmt.Fprint(stdout, l.String(logOpts))
			} else {
				fmt.Fprintln(stdout, l.String(logOpts))
			}
		}
	case "stderr":
		if stderr != nil {
			if l.Partial() {
				fmt.Fprint(stderr, l.String(logOpts))
			} else {
				fmt.Fprintln(stderr, l.String(logOpts))
			}
		}
	default:
		// Warn the user if the device type does not match. Most likely the file is corrupted.
		logrus.Warnf("Unknown Device type '%s' in log file from Container %s", l.Device, l.CID)
	}
}

// ReverseReader structure for reading a file backwards
type ReverseReader struct {
	reader   *os.File
	offset   int64
	readSize int64
}

// NewReverseReader returns a reader that reads from the end of a file
// rather than the beginning.  It sets the readsize to pagesize and determines
// the first offset using modulus.
func NewReverseReader(reader *os.File) (*ReverseReader, error) {
	// pagesize should be safe for memory use and file reads should be on page
	// boundaries as well
	pageSize := int64(os.Getpagesize())
	stat, err := reader.Stat()
	if err != nil {
		return nil, err
	}
	// figure out the last page boundary
	remainder := stat.Size() % pageSize
	end, err := reader.Seek(0, 2)
	if err != nil {
		return nil, err
	}
	// set offset (starting position) to the last page boundary or
	// zero if fits in one page
	startOffset := end - remainder
	if startOffset < 0 {
		startOffset = 0
	}
	rr := ReverseReader{
		reader:   reader,
		offset:   startOffset,
		readSize: pageSize,
	}
	return &rr, nil
}

// ReverseReader reads from a given offset to the previous offset and
// then sets the newoff set one pagesize less than the previous read.
func (r *ReverseReader) Read() (string, error) {
	if r.offset < 0 {
		return "", fmt.Errorf("at beginning of file: %w", io.EOF)
	}
	// Read from given offset
	b := make([]byte, r.readSize)
	n, err := r.reader.ReadAt(b, r.offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if int64(n) < r.readSize {
		b = b[0:n]
	}
	// Move the offset one pagesize up
	r.offset -= r.readSize
	return string(b), nil
}
