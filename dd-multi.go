// BSD 3-Clause License
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
// *Redistributions of source code must retain the above copyright notice, this
//  list of conditions and the following disclaimer.
//
// *Redistributions in binary form must reproduce the above copyright notice,
//  this list of conditions and the following disclaimer in the documentation
//  and/or other materials provided with the distribution.
//
// *Neither the name of the copyright holder nor the names of its
//  contributors may be used to endorse or promote products derived from
//  this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
// FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	// Added import for terminal size
	"golang.org/x/term"
)

// ANSI color codes
const (
	Reset      = "\033[0m"
	DarkGreen  = "\033[32m"
	LightGreen = "\033[92m"
	Grey       = "\033[90m"
)

const (
	DefaultCols  = 80
	DefaultRows  = 24
	MaxTransfers = 50
)

// Global vars for fullscreen mode & terminal size
var (
	fullscreen   bool
	terminalCols = DefaultCols
	terminalRows = DefaultRows
)

// bitClearAndSet is used for conv=, oflag= mappings
type bitClearAndSet struct {
	clear int
	set   int
}

// convMap, flagMap define possible conv=, oflag= values
var convMap = map[string]bitClearAndSet{
	"notrunc": {clear: os.O_TRUNC},
}

var flagMap = map[string]bitClearAndSet{
	"sync": {set: os.O_SYNC},
}

var allowedFlags = os.O_TRUNC | os.O_SYNC

// Transfer holds parameters for one dd operation
type Transfer struct {
	InputFilename  string
	OutputFilename string

	Bs    int64
	Count int64
	Size  int64
	Skip  int64
	Seek  int64
	Conv  string
	Oflag int

	Total       int64
	Transferred int64

	StartTime time.Time
	EndTime   time.Time
	Mutex     sync.Mutex
	Finished  bool
}

// parseConvOflag interprets conv=, oflag= strings
func parseConvOflag(convStr, oflagStr string) (int, error) {
	flags := 0
	if convStr != "none" {
		for _, c := range strings.Split(convStr, ",") {
			if v, ok := convMap[c]; ok {
				flags &= ^v.clear
				flags |= v.set
			} else {
				return 0, fmt.Errorf("unknown conv=%s", c)
			}
		}
	}
	if oflagStr != "none" {
		for _, f := range strings.Split(oflagStr, ",") {
			if v, ok := flagMap[f]; ok {
				flags &= ^v.clear
				flags |= v.set
			} else {
				return 0, fmt.Errorf("unknown oflag=%s", f)
			}
		}
	}
	return flags, nil
}

// parseBlockSize interprets e.g. "4M", "512b", etc.
func parseBlockSize(sizeStr string, defaultSize int64) int64 {
	if sizeStr == "" {
		return defaultSize
	}
	lastChar := sizeStr[len(sizeStr)-1]
	multiplier := int64(1)
	switch lastChar {
	case 'k', 'K':
		multiplier = 1024
		sizeStr = sizeStr[:len(sizeStr)-1]
	case 'M':
		multiplier = 1024 * 1024
		sizeStr = sizeStr[:len(sizeStr)-1]
	case 'G':
		multiplier = 1024 * 1024 * 1024
		sizeStr = sizeStr[:len(sizeStr)-1]
	case 'b', 'B':
		multiplier = 512
		sizeStr = sizeStr[:len(sizeStr)-1]
	}
	val, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		log.Fatalf("Invalid block size: %s", sizeStr)
	}
	return val * multiplier
}

// doOneTransfer runs dd for one Transfer
func doOneTransfer(t *Transfer, stdin io.Reader) error {
	r, err := inFile(stdin, t.InputFilename, t.Bs, t.Size, t.Skip, t.Count, &t.Total)
	if err != nil {
		return err
	}
	w, err := outFile(os.Stdout, t.OutputFilename, t.Bs, t.Seek, t.Oflag)
	if err != nil {
		return err
	}
	return dd(r, w, t.Bs, &t.Transferred)
}

// dd copies data from r to w in chunks
func dd(r io.Reader, w io.Writer, inBufSize int64, bytesWritten *int64) error {
	if inBufSize == 0 {
		return fmt.Errorf("input buffer size is zero")
	}
	buf := make([]byte, inBufSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			_, writeErr := w.Write(buf[:n])
			if writeErr != nil {
				return fmt.Errorf("error writing: %w", writeErr)
			}
			*bytesWritten += int64(n)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading: %w", err)
		}
	}
	return nil
}

// inFile sets up the input with skip & limit
func inFile(stdin io.Reader, name string, bs, size int64, skip, count int64, totalOut *int64) (io.Reader, error) {
	if name == "" {
		r := stdin
		if skip > 0 {
			_, err := io.CopyN(io.Discard, r, skip*bs)
			if err != nil {
				return nil, fmt.Errorf("error skipping stdin: %w", err)
			}
		}
		if count != math.MaxInt64 {
			*totalOut = count * bs
			return io.LimitReader(r, *totalOut), nil
		} else if size > 0 {
			*totalOut = size
			return io.LimitReader(r, size), nil
		}
		return r, nil
	}

	in, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("error opening input %q: %w", name, err)
	}
	fi, err := in.Stat()
	if err != nil {
		in.Close()
		return nil, fmt.Errorf("error stating %q: %w", name, err)
	}
	if fi.Mode().IsRegular() {
		_, err := in.Seek(skip*bs, io.SeekStart)
		if err != nil {
			in.Close()
			return nil, fmt.Errorf("error seeking %q: %w", name, err)
		}
		if count != math.MaxInt64 {
			*totalOut = count * bs
			return io.NewSectionReader(in, skip*bs, *totalOut), nil
		} else if size > 0 {
			*totalOut = size
			return io.NewSectionReader(in, skip*bs, size), nil
		} else {
			st, _ := in.Stat()
			*totalOut = st.Size() - (skip * bs)
			return in, nil
		}
	}
	// non-regular
	r := in
	if skip > 0 {
		_, err := io.CopyN(io.Discard, r, skip*bs)
		if err != nil {
			in.Close()
			return nil, fmt.Errorf("error skipping in %q: %w", name, err)
		}
	}
	if count != math.MaxInt64 {
		*totalOut = count * bs
		return io.LimitReader(r, *totalOut), nil
	} else if size > 0 {
		*totalOut = size
		return io.LimitReader(r, size), nil
	}
	return r, nil
}

// outFile sets up output with seek & flags
func outFile(stdout io.WriteSeeker, name string, bs int64, seek int64, flags int) (io.Writer, error) {
	if name == "" {
		return stdout, nil
	}
	perm := os.O_CREATE | os.O_WRONLY | (flags & allowedFlags)
	f, err := os.OpenFile(name, perm, 0o666)
	if err != nil {
		return nil, fmt.Errorf("error opening output %q: %w", name, err)
	}
	if seek*bs != 0 {
		if _, err := f.Seek(seek*bs, io.SeekCurrent); err != nil {
			return nil, fmt.Errorf("error seeking %q: %w", name, err)
		}
	}
	return f, nil
}

func usage() {
	log.Fatal(`Multi-Transfer dd with up to 50 sets. Use -numTransfers=N to specify how many sets are actually used.
Example:
 ./dd_multi_n -numTransfers=3 \
   -if1=/dev/zero -of1=file1.img -bs1=4M -size1=1G ...
   -if2=/dev/urandom -of2=file2.img -bs2=1M -size2=2G ...
   -if3=input.iso -of3=device -count3=700 ...
`)
}

// Convert x=y to -x y for the flag package
func convertArgs(osArgs []string) []string {
	var args []string
	for _, v := range osArgs {
		l := strings.SplitN(v, "=", 2)
		if len(l) == 2 {
			l[0] = "-" + l[0]
		}
		args = append(args, l...)
	}
	return args
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(stdin io.Reader, stdout io.WriteSeeker) error {
	// We'll define up to MaxTransfers sets of flags
	f := flag.NewFlagSet("dd_multi_n", flag.ExitOnError)

	numTransfers := f.Int("numTransfers", 0, "Number of parallel transfers (1..50)")

	// New fullscreen flag
	fsFullscreen := f.Bool("fullscreen", false, "Center progress bar(s) in fullscreen mode")

	// We'll store each set in slices
	inputFiles := make([]string, MaxTransfers)
	outputFiles := make([]string, MaxTransfers)
	bsVals := make([]string, MaxTransfers)
	convVals := make([]string, MaxTransfers)
	oflagVals := make([]string, MaxTransfers)

	countVals := make([]int64, MaxTransfers)
	skipVals := make([]int64, MaxTransfers)
	seekVals := make([]int64, MaxTransfers)
	sizeVals := make([]int64, MaxTransfers)

	// Pre-define all flags so that we won't get "flag provided but not defined"
	for i := 1; i <= MaxTransfers; i++ {
		f.StringVar(&inputFiles[i-1], fmt.Sprintf("if%d", i), "",
			fmt.Sprintf("Input file #%d", i))
		f.StringVar(&outputFiles[i-1], fmt.Sprintf("of%d", i), "",
			fmt.Sprintf("Output file #%d", i))
		f.StringVar(&bsVals[i-1], fmt.Sprintf("bs%d", i), "",
			fmt.Sprintf("Block size #%d", i))
		f.StringVar(&convVals[i-1], fmt.Sprintf("conv%d", i), "none",
			fmt.Sprintf("Conversions #%d", i))
		f.StringVar(&oflagVals[i-1], fmt.Sprintf("oflag%d", i), "none",
			fmt.Sprintf("Output flags #%d", i))

		f.Int64Var(&countVals[i-1], fmt.Sprintf("count%d", i), math.MaxInt64,
			fmt.Sprintf("Blocks #%d", i))
		f.Int64Var(&skipVals[i-1], fmt.Sprintf("skip%d", i), 0,
			fmt.Sprintf("Skip #%d blocks", i))
		f.Int64Var(&seekVals[i-1], fmt.Sprintf("seek%d", i), 0,
			fmt.Sprintf("Seek #%d blocks", i))
		f.Int64Var(&sizeVals[i-1], fmt.Sprintf("size%d", i), 0,
			fmt.Sprintf("Total bytes #%d", i))
	}

	f.Parse(convertArgs(os.Args[1:]))

	// If -fullscreen, attempt to get terminal size
	if *fsFullscreen {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil {
			terminalCols = w
			terminalRows = h
		}
		fullscreen = true
	}

	if *numTransfers <= 0 || *numTransfers > MaxTransfers {
		usage()
	}

	// Build the actual Transfer objects
	var transfers []*Transfer
	for i := 1; i <= *numTransfers; i++ {
		inName := inputFiles[i-1]
		outName := outputFiles[i-1]
		bsStr := bsVals[i-1]
		convStr := convVals[i-1]
		oflagStr := oflagVals[i-1]

		countVal := countVals[i-1]
		skipVal := skipVals[i-1]
		seekVal := seekVals[i-1]
		sizeVal := sizeVals[i-1]

		// If both inName/outName are empty, skip
		if inName == "" && outName == "" {
			continue
		}

		bsVal := parseBlockSize(bsStr, 512)
		flags, err := parseConvOflag(convStr, oflagStr)
		if err != nil {
			log.Printf("Error parsing conv/oflag for transfer #%d: %v", i, err)
			continue
		}

		t := &Transfer{
			InputFilename:  inName,
			OutputFilename: outName,
			Bs:             bsVal,
			Count:          countVal,
			Size:           sizeVal,
			Skip:           skipVal,
			Seek:           seekVal,
			Conv:           convStr,
			Oflag:          flags,
			StartTime:      time.Now(),
		}
		transfers = append(transfers, t)
	}

	if len(transfers) == 0 {
		usage()
	}

	// concurrency
	var ddWg sync.WaitGroup
	for _, t := range transfers {
		ddWg.Add(1)
		go func(tr *Transfer) {
			defer ddWg.Done()
			err := doOneTransfer(tr, stdin)
			if err != nil {
				log.Printf("Error in transfer %s->%s: %v", tr.InputFilename, tr.OutputFilename, err)
			}
			tr.Mutex.Lock()
			tr.Finished = true
			tr.EndTime = time.Now()
			tr.Mutex.Unlock()
		}(t)
	}

	// progress goroutine
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		mp := &MultiProgress{
			Transfers:  transfers,
			Fullscreen: fullscreen,
			TermCols:   terminalCols,
			TermRows:   terminalRows,
		}
		mp.startProgress()
	}()

	// handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigChan
		fmt.Fprintf(os.Stderr, "\nReceived signal: %s. Terminating gracefully...\n", s)
		for _, tr := range transfers {
			tr.Mutex.Lock()
			tr.Finished = true
			tr.EndTime = time.Now()
			tr.Mutex.Unlock()
		}
		os.Exit(1)
	}()

	ddWg.Wait()
	progressWg.Wait()
	return nil
}

// MultiProgress prints lines for multiple Transfers
type MultiProgress struct {
	Transfers  []*Transfer
	Fullscreen bool
	TermCols   int
	TermRows   int
}

func (mp *MultiProgress) startProgress() {
	linesPerTransfer := 2
	totalLines := linesPerTransfer * len(mp.Transfers)

	// If fullscreen, clear screen and vertically center if there's room
	if mp.Fullscreen {
		// Clear entire screen, move cursor to top-left
		fmt.Print("\033[2J\033[H")

		if mp.TermRows > totalLines {
			topMargin := (mp.TermRows - totalLines) / 2
			fmt.Print(strings.Repeat("\n", topMargin))
		}
	}

	// Initial print
	mp.printAll(false)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			allDone := true
			for _, tr := range mp.Transfers {
				tr.Mutex.Lock()
				done := tr.Finished
				tr.Mutex.Unlock()
				if !done {
					allDone = false
					break
				}
			}
			// Move cursor up to re-print the same lines
			fmt.Printf("\033[%dA", totalLines)
			mp.printAll(allDone)
			if allDone {
				return
			}
		}
	}
}

// printAll prints exactly 2 lines per transfer
func (mp *MultiProgress) printAll(finished bool) {
	for _, tr := range mp.Transfers {
		// line 1: banner
		banner := fmt.Sprintf("%s --> %s", tr.InputFilename, tr.OutputFilename)
		fmt.Println(centerText(banner, mp.TermCols))

		// line 2: progress
		tr.Mutex.Lock()
		transferred := tr.Transferred
		total := tr.Total
		isFinished := tr.Finished
		st := tr.StartTime
		et := tr.EndTime
		tr.Mutex.Unlock()

		var elapsed float64
		if isFinished {
			elapsed = et.Sub(st).Seconds()
		} else {
			elapsed = time.Since(st).Seconds()
		}

		var rate float64
		if elapsed > 0 {
			rate = float64(transferred) / (1024*1024) / elapsed
		}
		var pct float64
		if total > 0 {
			pct = float64(transferred) / float64(total) * 100
			if pct > 100 {
				pct = 100
			}
		}

		// Timer: final if done, else ETA
		var timerStr string
		if isFinished && pct >= 100 {
			h := int(elapsed / 3600)
			m := int((int(elapsed) % 3600) / 60)
			s := int(int(elapsed) % 60)
			timerStr = fmt.Sprintf("%02d:%02d:%02d", h, m, s)
		} else {
			timerStr = computeETA(transferred, total, elapsed, rate)
		}
		leftGrey := Grey + padRight(timerStr, 8) + Reset

		barWidth := 50
		filled := int((pct / 100) * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		filledBar := LightGreen + strings.Repeat("-", filled)
		unfilledBar := DarkGreen + strings.Repeat("-", barWidth-filled) + Reset
		bar := filledBar + unfilledBar

		rateStr := fmt.Sprintf("%.2f MB/s", rate)
		rateGrey := Grey + padLeft(rateStr, 12) + Reset

		leftSide := leftGrey + " " + bar + " "
		line := leftSide + rateGrey
		totalUsed := len(stripANSI(leftSide)) + len(stripANSI(rateGrey))

		extra := mp.TermCols - totalUsed
		if extra > 0 {
			line += strings.Repeat(" ", extra)
		}
		fmt.Printf("\r%s\n", line)
	}
}

// computeETA calculates time left or ?? if unknown
func computeETA(transferred, total int64, elapsed, rate float64) string {
	if rate <= 0 || total <= 0 || float64(transferred) >= float64(total) {
		return "??:??:??"
	}
	remainBytes := float64(total - transferred)
	remainMB := remainBytes / (1024 * 1024)
	remainSec := remainMB / rate
	if remainSec < 0 {
		return "??:??:??"
	}
	h := int(remainSec / 3600)
	m := int((int(remainSec) % 3600) / 60)
	s := int(int(remainSec) % 60)
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// stripANSI removes ANSI codes for length calculations
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1B && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			j++
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// padLeft ensures s is at least width wide, left-padded with spaces
func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// padRight ensures s is at least width wide, right-padded with spaces
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// centerText returns s centered in width columns
func centerText(s string, width int) string {
	if len(s) >= width {
		return s
	}
	spaces := (width - len(s)) / 2
	return strings.Repeat(" ", spaces) + s
}
