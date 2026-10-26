package main

import (
	"errors"
	"fmt"
	"strings"
)

// A small QR Code encoder (byte mode, error correction level M, versions 1-9, up to 180
// bytes) for the oven's Updates screen, which has no internet to fetch a QR library from.
// The layout follows ISO/IEC 18004. qr_test.go pins the output; it was checked by decoding
// every version with OpenCV's QR reader.

type qrBlocks struct {
	ec     int       // EC codewords per block
	groups [2][2]int // {count, data codewords per block}
}

var qrVersionsM = [10]qrBlocks{
	{},
	{10, [2][2]int{{1, 16}}},
	{16, [2][2]int{{1, 28}}},
	{26, [2][2]int{{1, 44}}},
	{18, [2][2]int{{2, 32}}},
	{24, [2][2]int{{2, 43}}},
	{16, [2][2]int{{4, 27}}},
	{18, [2][2]int{{4, 31}}},
	{22, [2][2]int{{2, 38}, {2, 39}}},
	{22, [2][2]int{{3, 36}, {2, 37}}},
}

var qrAlignment = [10][]int{nil, nil, {6, 18}, {6, 22}, {6, 26}, {6, 30}, {6, 34}, {6, 22, 38}, {6, 24, 42}, {6, 26, 46}}

func (b qrBlocks) dataCodewords() int {
	return b.groups[0][0]*b.groups[0][1] + b.groups[1][0]*b.groups[1][1]
}

// qrMatrix is row-major: m[row][col], true = dark.
type qrMatrix [][]bool

func qrEncode(text string) (qrMatrix, error) {
	data := []byte(text)
	version := 0
	for v := 1; v <= 9; v++ {
		// 4 mode bits + 8 count bits + the bytes.
		if 12+8*len(data) <= 8*qrVersionsM[v].dataCodewords() {
			version = v
			break
		}
	}
	if version == 0 {
		return nil, errors.New("text too long for a QR code")
	}
	blocks := qrVersionsM[version]
	capacity := blocks.dataCodewords()

	var bits []bool
	put := func(v, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (v>>i)&1 == 1)
		}
	}
	put(0b0100, 4)
	put(len(data), 8)
	for _, c := range data {
		put(int(c), 8)
	}
	for i := 0; i < 4 && len(bits) < capacity*8; i++ {
		bits = append(bits, false)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	codewords := make([]byte, 0, capacity)
	for i := 0; i < len(bits); i += 8 {
		var c byte
		for j := 0; j < 8; j++ {
			if bits[i+j] {
				c |= 1 << (7 - j)
			}
		}
		codewords = append(codewords, c)
	}
	for pad := 0; len(codewords) < capacity; pad++ {
		codewords = append(codewords, [2]byte{0xEC, 0x11}[pad%2])
	}

	// Split into blocks, add Reed-Solomon codewords, interleave.
	var dataBlocks, ecBlocks [][]byte
	off := 0
	gen := rsGenerator(blocks.ec)
	for _, g := range blocks.groups {
		for i := 0; i < g[0]; i++ {
			d := codewords[off : off+g[1]]
			off += g[1]
			dataBlocks = append(dataBlocks, d)
			ecBlocks = append(ecBlocks, rsRemainder(d, gen))
		}
	}
	var final []byte
	for i := 0; ; i++ {
		any := false
		for _, d := range dataBlocks {
			if i < len(d) {
				final = append(final, d[i])
				any = true
			}
		}
		if !any {
			break
		}
	}
	for i := 0; i < blocks.ec; i++ {
		for _, e := range ecBlocks {
			final = append(final, e[i])
		}
	}

	q := newQRBuilder(version)
	q.drawCodewords(final)
	best, bestPenalty := -1, 0
	for mask := 0; mask < 8; mask++ {
		q.applyMask(mask)
		q.drawFormat(mask)
		if p := q.penalty(); best < 0 || p < bestPenalty {
			best, bestPenalty = mask, p
		}
		q.applyMask(mask) // XOR again to undo
	}
	q.applyMask(best)
	q.drawFormat(best)
	return q.m, nil
}

type qrBuilder struct {
	size     int
	m        qrMatrix
	function [][]bool
}

func newQRBuilder(version int) *qrBuilder {
	size := 17 + 4*version
	q := &qrBuilder{size: size, m: make(qrMatrix, size), function: make([][]bool, size)}
	for i := range q.m {
		q.m[i] = make([]bool, size)
		q.function[i] = make([]bool, size)
	}
	for i := 0; i < size; i++ {
		q.set(6, i, i%2 == 0)
		q.set(i, 6, i%2 == 0)
	}
	q.finder(3, 3)
	q.finder(3, size-4)
	q.finder(size-4, 3)
	align := qrAlignment[version]
	for _, r := range align {
		for _, c := range align {
			if (r == 6 && c == 6) || (r == 6 && c == align[len(align)-1]) || (r == align[len(align)-1] && c == 6) {
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					q.set(r+dr, c+dc, max(abs(dr), abs(dc)) != 1)
				}
			}
		}
	}
	q.drawFormat(0) // reserves the format areas; redrawn once the mask is chosen
	if version >= 7 {
		rem := version
		for i := 0; i < 12; i++ {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		bits := version<<12 | rem
		for i := 0; i < 18; i++ {
			bit := (bits>>i)&1 == 1
			a, b := size-11+i%3, i/3
			q.set(b, a, bit)
			q.set(a, b, bit)
		}
	}
	return q
}

func (q *qrBuilder) set(row, col int, dark bool) {
	q.m[row][col] = dark
	q.function[row][col] = true
}

func (q *qrBuilder) finder(row, col int) {
	for dr := -4; dr <= 4; dr++ {
		for dc := -4; dc <= 4; dc++ {
			r, c := row+dr, col+dc
			if r < 0 || r >= q.size || c < 0 || c >= q.size {
				continue
			}
			d := max(abs(dr), abs(dc))
			q.set(r, c, d != 2 && d != 4)
		}
	}
}

// drawFormat writes the 15 format bits (level M = 00, then the mask) in both copies.
func (q *qrBuilder) drawFormat(mask int) {
	data := mask // level M is 00
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	s := q.size
	for i := 0; i <= 5; i++ {
		q.set(i, 8, bit(i))
	}
	q.set(7, 8, bit(6))
	q.set(8, 8, bit(7))
	q.set(8, 7, bit(8))
	for i := 9; i < 15; i++ {
		q.set(8, 14-i, bit(i))
	}
	for i := 0; i < 8; i++ {
		q.set(8, s-1-i, bit(i))
	}
	for i := 8; i < 15; i++ {
		q.set(s-15+i, 8, bit(i))
	}
	q.set(s-8, 8, true) // the dark module
}

func (q *qrBuilder) drawCodewords(data []byte) {
	i := 0
	for right := q.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < q.size; vert++ {
			for j := 0; j < 2; j++ {
				col := right - j
				upward := ((right + 1) & 2) == 0
				row := vert
				if upward {
					row = q.size - 1 - vert
				}
				if !q.function[row][col] && i < len(data)*8 {
					q.m[row][col] = (data[i>>3]>>(7-uint(i&7)))&1 == 1
					i++
				}
			}
		}
	}
}

func (q *qrBuilder) applyMask(mask int) {
	for r := 0; r < q.size; r++ {
		for c := 0; c < q.size; c++ {
			if q.function[r][c] {
				continue
			}
			var flip bool
			switch mask {
			case 0:
				flip = (r+c)%2 == 0
			case 1:
				flip = r%2 == 0
			case 2:
				flip = c%3 == 0
			case 3:
				flip = (r+c)%3 == 0
			case 4:
				flip = (r/2+c/3)%2 == 0
			case 5:
				flip = (r*c)%2+(r*c)%3 == 0
			case 6:
				flip = ((r*c)%2+(r*c)%3)%2 == 0
			case 7:
				flip = ((r+c)%2+(r*c)%3)%2 == 0
			}
			if flip {
				q.m[r][c] = !q.m[r][c]
			}
		}
	}
}

// penalty scores a masked symbol by the standard's four rules; lower is easier to scan.
func (q *qrBuilder) penalty() int {
	s, p := q.size, 0
	at := func(r, c int, byRow bool) bool {
		if byRow {
			return q.m[r][c]
		}
		return q.m[c][r]
	}
	finderA := []bool{true, false, true, true, true, false, true, false, false, false, false}
	finderB := []bool{false, false, false, false, true, false, true, true, true, false, true}
	for _, byRow := range []bool{true, false} {
		for r := 0; r < s; r++ {
			run := 1
			for c := 1; c <= s; c++ {
				if c < s && at(r, c, byRow) == at(r, c-1, byRow) {
					run++
					continue
				}
				if run >= 5 {
					p += 3 + run - 5
				}
				run = 1
			}
			for c := 0; c+11 <= s; c++ {
				a, b := true, true
				for k := 0; k < 11; k++ {
					v := at(r, c+k, byRow)
					a = a && v == finderA[k]
					b = b && v == finderB[k]
				}
				if a {
					p += 40
				}
				if b {
					p += 40
				}
			}
		}
	}
	dark := 0
	for r := 0; r < s; r++ {
		for c := 0; c < s; c++ {
			if q.m[r][c] {
				dark++
			}
			if r+1 < s && c+1 < s {
				v := q.m[r][c]
				if q.m[r][c+1] == v && q.m[r+1][c] == v && q.m[r+1][c+1] == v {
					p += 3
				}
			}
		}
	}
	pct := dark * 100 / (s * s)
	p += abs(pct-50) / 5 * 10
	return p
}

func rsGenerator(degree int) []byte {
	g := []byte{1}
	root := byte(1)
	for i := 0; i < degree; i++ {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, root)
		}
		g = next
		root = gfMul(root, 2)
	}
	return g[1:]
}

func rsRemainder(data, gen []byte) []byte {
	rem := make([]byte, len(gen))
	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[len(rem)-1] = 0
		for i, g := range gen {
			rem[i] ^= gfMul(g, factor)
		}
	}
	return rem
}

func gfMul(x, y byte) byte {
	var z int
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		if (y>>uint(i))&1 == 1 {
			z ^= int(x)
		}
	}
	return byte(z)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// qrSVG renders the matrix with a 4-module quiet zone, dark modules as one path.
func qrSVG(m qrMatrix) string {
	n := len(m) + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for r, row := range m {
		for c, dark := range row {
			if dark {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", c+4, r+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}
