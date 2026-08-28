package kube

import (
	"cmp"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Table buffers columnar rows and renders them aligned on visible width (ANSI
// escape codes are ignored when measuring), optionally sorted by a named
// column. Headers are bolded when the painter is enabled.
type Table struct {
	out       io.Writer
	painter   Painter
	headers   []string
	rows      [][]string
	sortCol   string
	sortRanks map[string]func(string) int
	arena     []string
}

// NewTable starts a table with the given painter and header row.
func NewTable(out io.Writer, p Painter, headers ...string) *Table {
	return &Table{out: out, painter: p, headers: headers}
}

// rowArena is how many cells each backing block holds. Row copies into a slice
// of a shared block rather than allocating per row, which on a 20k-row table
// trades 20k allocations for a few dozen. A row is never appended to after Row
// returns (Flush and sortRows only read cells and reorder the outer slice), so
// the blocks can be shared; the three-index slice pins each row's capacity so a
// future append would copy instead of scribbling on the next row.
const rowArena = 4096

// Row appends one data row, copying cols so the caller keeps ownership of it.
func (t *Table) Row(cols ...string) {
	if len(t.arena) < len(cols) {
		t.arena = make([]string, max(rowArena, len(cols)))
	}
	row := t.arena[:len(cols):len(cols)]
	t.arena = t.arena[len(cols):]
	copy(row, cols)
	t.rows = append(t.rows, row)
}

// SortBy sorts rows ascending by the named column (case-insensitive match
// against the headers), detecting numeric columns so counts order naturally.
// An empty column name or one absent from the headers is a no-op.
func (t *Table) SortBy(column string) {
	t.sortCol = column
}

// SortRank registers a custom sort key for a column, overriding the default
// text/numeric ordering when the table is sorted by it. Rows are ordered by the
// returned key ascending. Used for severity columns (e.g. a VERDICT column)
// where alphabetical order is meaningless and the rows should read worst-first.
func (t *Table) SortRank(column string, key func(cell string) int) {
	if t.sortRanks == nil {
		t.sortRanks = map[string]func(string) int{}
	}
	t.sortRanks[strings.ToLower(column)] = key
}

const tableGap = 2

// Flush renders the table, applying the sort column if one was set. Columns are
// padded to their widest visible cell plus a fixed gap; the last column is not
// padded (no trailing whitespace).
func (t *Table) Flush() error {
	if idx := t.columnIndex(t.sortCol); idx >= 0 {
		t.sortRows(idx)
	}
	widths := make([]int, len(t.headers))
	bytes := 0
	for i, h := range t.headers {
		widths[i] = visibleWidth(h)
		bytes += len(h)
	}
	for _, r := range t.rows {
		for i := 0; i < len(widths) && i < len(r); i++ {
			bytes += len(r[i])
			if w := visibleWidth(r[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var b strings.Builder
	b.Grow(bytes + (len(t.rows)+1)*(maxPadding(widths)+1))
	t.writeLine(&b, widths, t.headers, true)
	for _, r := range t.rows {
		t.writeLine(&b, widths, r, false)
	}
	_, err := io.WriteString(t.out, b.String())
	return err
}

// maxPadding bounds the whitespace one line can need: a cell contributes at most
// its column width plus the gap, when the cell renders empty. Overshooting is
// the point - Flush uses it to size the Builder in one Grow instead of letting
// it double its way up, which on a 20k-row table was the single largest
// allocator in the process.
func maxPadding(widths []int) int {
	total := 0
	for _, w := range widths[:max(0, len(widths)-1)] {
		total += w + tableGap
	}
	return total
}

// writeLine renders one row, padding each column (except the last) to its width
// based on visible content, so embedded ANSI codes don't shift columns.
func (t *Table) writeLine(b *strings.Builder, widths []int, cells []string, header bool) {
	last := len(t.headers) - 1
	for i := 0; i < len(t.headers); i++ {
		c := cell(cells, i)
		if header {
			c = t.painter.Header(c)
		}
		b.WriteString(c)
		if i < last {
			b.WriteString(strings.Repeat(" ", widths[i]-visibleWidth(c)+tableGap))
		}
	}
	b.WriteByte('\n')
}

// sortRows orders rows ascending by column idx: a registered SortRank key if the
// column has one, else by numeric value when every cell parses as a number, else
// lexically.
//
// Keys are extracted once per row instead of inside the comparator. A comparator
// runs O(n log n) times and each call used to strip ANSI from two cells, so a
// 20k-row table paid ~570k string allocations to sort; this pays n.
func (t *Table) sortRows(idx int) {
	keys := make([]sortKey, len(t.rows))
	for i, r := range t.rows {
		keys[i] = sortKey{row: r, text: stripANSI(cell(r, idx))}
	}
	if rank := t.sortRanks[strings.ToLower(t.sortCol)]; rank != nil {
		for i := range keys {
			keys[i].num = float64(rank(keys[i].text))
		}
		slices.SortStableFunc(keys, byNum)
	} else if t.parseNumeric(keys) {
		slices.SortStableFunc(keys, byNum)
	} else {
		slices.SortStableFunc(keys, func(a, b sortKey) int { return cmp.Compare(a.text, b.text) })
	}
	for i := range keys {
		t.rows[i] = keys[i].row
	}
}

// sortKey is a row paired with its extracted sort key.
type sortKey struct {
	row  []string
	text string
	num  float64
}

func byNum(a, b sortKey) int { return cmp.Compare(a.num, b.num) }

// parseNumeric fills in each key's num and reports whether every cell parsed, so
// counts order by value rather than as text. An empty table is not numeric.
func (t *Table) parseNumeric(keys []sortKey) bool {
	if len(keys) == 0 {
		return false
	}
	for i := range keys {
		f, err := strconv.ParseFloat(keys[i].text, 64)
		if err != nil {
			return false
		}
		keys[i].num = f
	}
	return true
}

func (t *Table) columnIndex(column string) int {
	if column == "" {
		return -1
	}
	for i, h := range t.headers {
		if strings.EqualFold(h, column) {
			return i
		}
	}
	return -1
}

func cell(row []string, idx int) string {
	if idx < len(row) {
		return row[idx]
	}
	return ""
}

// Label returns the value of key in labels, or a muted "<none>" when
// absent/empty.
func Label(p Painter, labels map[string]string, key string) string {
	if v, ok := labels[key]; ok && v != "" {
		return v
	}
	return p.Muted("<none>")
}
