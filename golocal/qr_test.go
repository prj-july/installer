package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

const qrTypicalURL = "https://192.168.1.23/local#updates&code=ABCD2345"

func qrCheckFinder(t *testing.T, m qrMatrix, row, col int) {
	t.Helper()
	for dr := -3; dr <= 3; dr++ {
		for dc := -3; dc <= 3; dc++ {
			d := max(abs(dr), abs(dc))
			if want := d != 2; m[row+dr][col+dc] != want {
				t.Fatalf("finder at (%d,%d): module (%d,%d) = %t", row, col, row+dr, col+dc, !want)
			}
		}
	}
}

func qrFormatCopies(m qrMatrix) (int, int) {
	s := len(m)
	var a, b int
	setA := func(i int, v bool) {
		if v {
			a |= 1 << i
		}
	}
	setB := func(i int, v bool) {
		if v {
			b |= 1 << i
		}
	}
	for i := 0; i <= 5; i++ {
		setA(i, m[i][8])
	}
	setA(6, m[7][8])
	setA(7, m[8][8])
	setA(8, m[8][7])
	for i := 9; i < 15; i++ {
		setA(i, m[8][14-i])
	}
	for i := 0; i < 8; i++ {
		setB(i, m[8][s-1-i])
	}
	for i := 8; i < 15; i++ {
		setB(i, m[s-15+i][8])
	}
	return a, b
}

func qrBCHRemainder(v, poly, polyDegree, totalBits int) int {
	for i := totalBits - 1; i >= polyDegree; i-- {
		if v&(1<<i) != 0 {
			v ^= poly << (i - polyDegree)
		}
	}
	return v
}

func TestQREncodeStructure(t *testing.T) {
	cases := []struct {
		text    string
		version int
	}{
		{"A", 1},
		{strings.Repeat("a", 14), 1},
		{strings.Repeat("b", 15), 2},
		{qrTypicalURL, 4},
		{strings.Repeat("c", 50), 4},
		{strings.Repeat("d", 70), 5},
		{strings.Repeat("e", 110), 7},
		{strings.Repeat("f", 140), 8},
		{strings.Repeat("g", 170), 9},
		{strings.Repeat("h", 180), 9},
	}
	for _, c := range cases {
		m, err := qrEncode(c.text)
		if err != nil {
			t.Fatalf("%d bytes: %v", len(c.text), err)
		}
		s := len(m)
		if s != 17+4*c.version {
			t.Fatalf("%d bytes: size %d, want version %d (%d)", len(c.text), s, c.version, 17+4*c.version)
		}
		for _, row := range m {
			if len(row) != s {
				t.Fatalf("%d bytes: ragged matrix", len(c.text))
			}
		}
		qrCheckFinder(t, m, 3, 3)
		qrCheckFinder(t, m, 3, s-4)
		qrCheckFinder(t, m, s-4, 3)
		for i := 8; i < s-8; i++ {
			if m[6][i] != (i%2 == 0) || m[i][6] != (i%2 == 0) {
				t.Fatalf("%d bytes: timing pattern broken at %d", len(c.text), i)
			}
		}
		if !m[s-8][8] {
			t.Fatalf("%d bytes: dark module missing", len(c.text))
		}
		a, b := qrFormatCopies(m)
		if a != b {
			t.Fatalf("%d bytes: format copies differ: %015b vs %015b", len(c.text), a, b)
		}
		f := a ^ 0x5412
		if qrBCHRemainder(f, 0x537, 10, 15) != 0 {
			t.Fatalf("%d bytes: format info %015b fails its BCH check", len(c.text), a)
		}
		if level := f >> 13; level != 0 {
			t.Fatalf("%d bytes: level bits %02b, want 00 (M)", len(c.text), level)
		}
		if c.version >= 7 {
			var v1, v2 int
			for i := 0; i < 18; i++ {
				ra, rb := s-11+i%3, i/3
				if m[rb][ra] {
					v1 |= 1 << i
				}
				if m[ra][rb] {
					v2 |= 1 << i
				}
			}
			if v1 != v2 || v1>>12 != c.version || qrBCHRemainder(v1, 0x1F25, 12, 18) != 0 {
				t.Fatalf("%d bytes: version info %018b / %018b", len(c.text), v1, v2)
			}
		}
	}
}

func TestQREncodeTooLong(t *testing.T) {
	if _, err := qrEncode(strings.Repeat("x", 181)); err == nil {
		t.Fatal("181 bytes should not fit version 9-M")
	}
}

// Pinned output for the link the oven screen shows. This exact matrix was decoded back to
// the URL with OpenCV's QR reader before it was pinned.
func TestQRGolden(t *testing.T) {
	m, err := qrEncode(qrTypicalURL)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, row := range m {
		for _, dark := range row {
			if dark {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	if got := hex.EncodeToString(sum[:]); got != qrGoldenSHA256 {
		t.Fatalf("QR matrix changed: sha256 %s", got)
	}
}

const qrGoldenSHA256 = "525b401c53c2f61555510120da6246c35fba4a109bdc11b46b83a3959ce8682a"

func TestQRSVG(t *testing.T) {
	m, err := qrEncode(qrTypicalURL)
	if err != nil {
		t.Fatal(err)
	}
	svg := qrSVG(m)
	dec := xml.NewDecoder(strings.NewReader(svg))
	for {
		if _, err := dec.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("qrSVG is not well-formed XML: %v", err)
		}
	}
	if !strings.Contains(svg, `viewBox="0 0 41 41"`) {
		t.Fatalf("want a 33-module symbol plus a 4-module quiet zone: %.80s", svg)
	}
	if strings.Contains(svg, "M0 ") || strings.Contains(svg, "M3 ") || !strings.Contains(svg, "M4 4h1v1h-1z") {
		t.Fatal("dark modules must start inside the 4-module quiet zone")
	}
}
