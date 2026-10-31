package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type Process struct {
	PID   int          `json:"pid"`
	PPID  int          `json:"ppid"`
	Name  string       `json:"name"`
	State string       `json:"state"`
	Ticks uint64       `json:"ticks"`
	Start uint64       `json:"start"`
	RSS   int64        `json:"rss_bytes"`
	CPU   float64      `json:"cpu_percent"`
	X     float64      `json:"-"`
	Y     float64      `json:"-"`
	VX    float64      `json:"-"`
	VY    float64      `json:"-"`
	Seen  bool         `json:"-"`
	Glow  int          `json:"-"`
	Trail []TrailPoint `json:"-"`
}
type TrailPoint struct{ X, Y float64 }
type Event struct {
	Time string `json:"time"`
	Kind string `json:"kind"`
	PID  int    `json:"pid"`
	Name string `json:"name"`
}
type Universe struct {
	Processes     map[int]*Process
	Events        []Event
	PreviousTotal uint64
	Frame         int
	Width         int
	Height        int
	Focus         int
	ShowNames     bool
	ShowLinks     bool
	Color         bool
	MaxNodes      int
	Filter        string
	Root          int
	Seed          int64
	LastSample    time.Time
	Spawned       int
	Exited        int
	Palette       string
	Trails        bool
	Orbit         bool
}

func clamp(x, a, b float64) float64 {
	if x < a {
		return a
	}
	if x > b {
		return b
	}
	return x
}
func parseProcess(pid int) (*Process, error) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return nil, e
	}
	s := string(b)
	a := strings.IndexByte(s, '(')
	z := strings.LastIndexByte(s, ')')
	if a < 0 || z < a {
		return nil, fmt.Errorf("invalid stat")
	}
	f := strings.Fields(s[z+1:])
	if len(f) < 22 {
		return nil, fmt.Errorf("short stat")
	}
	ppid, _ := strconv.Atoi(f[1])
	ut, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	start, _ := strconv.ParseUint(f[19], 10, 64)
	rss, _ := strconv.ParseInt(f[21], 10, 64)
	return &Process{PID: pid, PPID: ppid, Name: s[a+1 : z], State: f[0], Ticks: ut + st, Start: start, RSS: rss * int64(os.Getpagesize())}, nil
}
func totalCPU() uint64 {
	f, e := os.Open("/proc/stat")
	if e != nil {
		return 0
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	if !s.Scan() {
		return 0
	}
	var total uint64
	for _, v := range strings.Fields(s.Text())[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
	}
	return total
}
func terminalSize() (int, int) {
	w, h := 100, 32
	var ws struct{ Rows, Cols, X, Y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno == 0 && ws.Cols > 25 && ws.Rows > 12 {
		return int(ws.Cols), int(ws.Rows)
	}
	if v, e := strconv.Atoi(os.Getenv("COLUMNS")); e == nil && v > 25 {
		w = v
	}
	if v, e := strconv.Atoi(os.Getenv("LINES")); e == nil && v > 12 {
		h = v
	}
	return w, h
}
func newUniverse() *Universe {
	return &Universe{Processes: make(map[int]*Process), ShowNames: true, ShowLinks: true, Color: true, MaxNodes: 500}
}
func (u *Universe) addEvent(kind string, p *Process) {
	u.Events = append(u.Events, Event{Time: time.Now().Format("15:04:05"), Kind: kind, PID: p.PID, Name: p.Name})
	if len(u.Events) > 30 {
		u.Events = u.Events[len(u.Events)-30:]
	}
}
func (u *Universe) sample() {
	for _, p := range u.Processes {
		p.Seen = false
	}
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return
	}
	total := totalCPU()
	delta := uint64(0)
	if total > u.PreviousTotal {
		delta = total - u.PreviousTotal
	}
	u.PreviousTotal = total
	u.Spawned = 0
	u.Exited = 0
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		pid, e := strconv.Atoi(d.Name())
		if e != nil {
			continue
		}
		p, e := parseProcess(pid)
		if e != nil {
			continue
		}
		if old, ok := u.Processes[pid]; ok && old.Start == p.Start {
			old.CPU = 0
			if delta > 0 && p.Ticks >= old.Ticks {
				old.CPU = 100 * float64(p.Ticks-old.Ticks) / float64(delta)
			}
			old.Ticks = p.Ticks
			old.PPID = p.PPID
			old.Name = p.Name
			old.State = p.State
			old.RSS = p.RSS
			old.Seen = true
			if old.Glow > 0 {
				old.Glow--
			}
		} else {
			if ok {
				u.addEvent("exit", old)
				u.Exited++
			}
			angle := float64(pid) * 2.399963229728653
			rad := 0.05 + 0.38*math.Sqrt(float64(pid%997)/997)
			p.X = 0.5 + rad*math.Cos(angle)
			p.Y = 0.5 + rad*math.Sin(angle)
			p.VX = 0.001 * math.Sin(angle)
			p.VY = 0.001 * math.Cos(angle)
			p.Seen = true
			p.Glow = 12
			u.Processes[pid] = p
			u.Spawned++
			if u.Frame > 0 {
				u.addEvent("spawn", p)
			}
		}
	}
	for pid, p := range u.Processes {
		if !p.Seen {
			u.addEvent("exit", p)
			delete(u.Processes, pid)
			u.Exited++
		}
	}
	u.LastSample = time.Now()
}
func (u *Universe) visible() []*Process {
	out := make([]*Process, 0, len(u.Processes))
	for _, p := range u.Processes {
		if u.Filter != "" && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(u.Filter)) {
			continue
		}
		if u.Root > 0 && !u.descendant(p.PID, u.Root) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CPU == out[j].CPU {
			return out[i].RSS > out[j].RSS
		}
		return out[i].CPU > out[j].CPU
	})
	if u.MaxNodes > 0 && len(out) > u.MaxNodes {
		out = out[:u.MaxNodes]
	}
	return out
}
func (u *Universe) descendant(pid, root int) bool {
	for i := 0; i < 1000; i++ {
		if pid == root {
			return true
		}
		p, ok := u.Processes[pid]
		if !ok || p.PPID == pid || p.PPID == 0 {
			return false
		}
		pid = p.PPID
	}
	return false
}
func (u *Universe) layout(nodes []*Process) {
	cells := make(map[[2]int][]*Process)
	for _, p := range nodes {
		key := [2]int{int(p.X * 24), int(p.Y * 16)}
		cells[key] = append(cells[key], p)
	}
	for _, p := range nodes {
		angle := float64(p.PID) * 2.399963229728653
		tx, ty := 0.5+0.32*math.Cos(angle), 0.5+0.32*math.Sin(angle)
		if parent, ok := u.Processes[p.PPID]; ok && parent.PID != p.PID {
			tx = parent.X + 0.105*math.Cos(angle)
			ty = parent.Y + 0.16*math.Sin(angle)
		}
		fx, fy := (tx-p.X)*0.007, (ty-p.Y)*0.007
		cx, cy := int(p.X*24), int(p.Y*16)
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				for _, q := range cells[[2]int{cx + dx, cy + dy}] {
					if q == p {
						continue
					}
					xx, yy := p.X-q.X, (p.Y-q.Y)*1.4
					d := xx*xx + yy*yy + 0.000025
					if d < 0.004 {
						rep := 0.000014 / d
						fx += xx * rep
						fy += yy * rep
					}
				}
			}
		}
		if u.Orbit {
			fx += -(p.Y - 0.5) * 0.00022
			fy += (p.X - 0.5) * 0.00022
		}
		p.VX = clamp((p.VX+fx)*0.94, -0.012, 0.012)
		p.VY = clamp((p.VY+fy)*0.94, -0.012, 0.012)
		if u.Trails {
			if u.Frame%3 == 0 {
				p.Trail = append(p.Trail, TrailPoint{p.X, p.Y})
				if len(p.Trail) > 7 {
					p.Trail = p.Trail[1:]
				}
			}
		}
		p.X = clamp(p.X+p.VX, 0.025, 0.94)
		p.Y = clamp(p.Y+p.VY, 0.05, 0.94)
	}
}

type Pixel struct {
	R        rune
	Color    int
	Priority int
}

func pixel(g [][]Pixel, x, y int, r rune, color, priority int) {
	if y >= 0 && y < len(g) && x >= 0 && x < len(g[y]) && priority >= g[y][x].Priority {
		g[y][x] = Pixel{r, color, priority}
	}
}
func galaxyLine(g [][]Pixel, x, y, xx, yy int, color int) {
	dx := int(math.Abs(float64(xx - x)))
	dy := -int(math.Abs(float64(yy - y)))
	sx, sy := -1, -1
	if x < xx {
		sx = 1
	}
	if y < yy {
		sy = 1
	}
	err := dx + dy
	for i := 0; i < 600; i++ {
		pixel(g, x, y, '·', color, 1)
		if x == xx && y == yy {
			break
		}
		e := 2 * err
		if e >= dy {
			err += dy
			x += sx
		}
		if e <= dx {
			err += dx
			y += sy
		}
	}
}
func trim(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func (u *Universe) render() string {
	w, h := terminalSize()
	if w > 220 {
		w = 220
	}
	if h > 75 {
		h = 75
	}
	if w < 30 {
		w = 30
	}
	if h < 14 {
		h = 14
	}
	u.Width = w
	u.Height = h
	ah := h - 8
	grid := make([][]Pixel, ah)
	for y := range grid {
		grid[y] = make([]Pixel, w)
		for x := range grid[y] {
			grid[y][x] = Pixel{' ', 0, 0}
		}
	}
	nodes := u.visible()
	u.layout(nodes)
	included := make(map[int]bool, len(nodes))
	for _, p := range nodes {
		included[p.PID] = true
	}
	at := func(p *Process) (int, int) { return int(p.X * float64(w-1)), int(p.Y * float64(ah-1)) }
	if u.Trails {
		for _, p := range nodes {
			for i, t := range p.Trail {
				x, y := int(t.X*float64(w-1)), int(t.Y*float64(ah-1))
				c := 24 + i*2
				pixel(grid, x, y, '·', c, 2)
			}
		}
	}
	if u.ShowLinks {
		for _, p := range nodes {
			if parent, ok := u.Processes[p.PPID]; ok && included[parent.PID] && parent.PID != p.PID {
				x, y := at(p)
				xx, yy := at(parent)
				galaxyLine(grid, x, y, xx, yy, u.linkColor())
			}
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].RSS < nodes[j].RSS })
	for _, p := range nodes {
		x, y := at(p)
		ch := '•'
		c := u.nodeColor(p)
		if p.State == "R" {
			ch = '◆'
		}
		if p.RSS > 256<<20 {
			ch = '●'
		}
		if p.Glow > 0 {
			ch = '✦'
			c = 226
		}
		if p.PID == u.Focus {
			ch = '★'
			c = 231
		}
		pixel(grid, x, y, ch, c, 10)
		if u.ShowNames && (p.PID == 1 || p.PID == u.Focus || p.CPU > 0.5 || p.RSS > 256<<20) {
			for j, r := range trim(p.Name, 11) {
				pixel(grid, x+j+2, y, r, c, 5)
			}
		}
	}
	var b strings.Builder
	b.WriteString("\x1b[H")
	fmt.Fprintf(&b, " PROC UNIVERSE  ✦  %s   processes %d   visible %d   frame %d\x1b[K\n", time.Now().Format("15:04:05"), len(u.Processes), len(nodes), u.Frame)
	b.WriteString(strings.Repeat("─", w) + "\n")
	for _, row := range grid {
		prev := -1
		for _, v := range row {
			if u.Color && v.Color != prev {
				if v.Color == 0 {
					b.WriteString("\x1b[0m")
				} else {
					fmt.Fprintf(&b, "\x1b[38;5;%dm", v.Color)
				}
				prev = v.Color
			}
			b.WriteRune(v.R)
		}
		b.WriteString("\x1b[0m\x1b[K\n")
	}
	b.WriteString(strings.Repeat("─", w) + "\n")
	fmt.Fprintf(&b, " ✦ newborn  ◆ running  ● memory  · lineage  |  +%d spawned  -%d exited\x1b[K\n", u.Spawned, u.Exited)
	fmt.Fprintf(&b, " Palette %s | orbit %t | trails %t | Ctrl+C quit\x1b[K\n", u.Palette, u.Orbit, u.Trails)
	if len(u.Events) > 0 {
		e := u.Events[len(u.Events)-1]
		fmt.Fprintf(&b, " Event %s %s PID=%d %s", e.Time, e.Kind, e.PID, trim(e.Name, 24))
	}
	b.WriteString("\x1b[K\n")
	return b.String()
}
func (u *Universe) linkColor() int {
	if u.Palette == "ember" {
		return 94
	}
	if u.Palette == "aurora" {
		return 23
	}
	return 60
}
func (u *Universe) nodeColor(p *Process) int {
	if u.Palette == "ember" {
		if p.CPU > 0.5 {
			return 196
		}
		if p.RSS > 128<<20 {
			return 208
		}
		return 220
	}
	if u.Palette == "aurora" {
		if p.CPU > 0.5 {
			return 201
		}
		if p.RSS > 128<<20 {
			return 48
		}
		return 81
	}
	if p.CPU > 0.5 {
		return 213
	}
	if p.RSS > 128<<20 {
		return 51
	}
	return 117
}
func printSummary(u *Universe) {
	nodes := u.visible()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].RSS > nodes[j].RSS })
	fmt.Printf("%-8s %-8s %-5s %-10s %-10s %s\n", "PID", "PPID", "STATE", "RSS MiB", "CPU %", "COMMAND")
	for i, p := range nodes {
		if i >= 60 {
			break
		}
		fmt.Printf("%-8d %-8d %-5s %-10.1f %-10.2f %s\n", p.PID, p.PPID, p.State, float64(p.RSS)/(1<<20), p.CPU, p.Name)
	}
}
func main() {
	interval := flag.Duration("interval", 150*time.Millisecond, "animation frame interval")
	sample := flag.Duration("sample", time.Second, "procfs sampling interval")
	names := flag.Bool("names", true, "show selected process labels")
	links := flag.Bool("links", true, "show process lineage")
	once := flag.Bool("once", false, "print process summary")
	jsonMode := flag.Bool("json", false, "print JSON snapshot")
	frames := flag.Int("frames", 0, "stop after number of frames")
	maxNodes := flag.Int("max-nodes", 500, "maximum rendered processes")
	filter := flag.String("filter", "", "process name substring filter")
	root := flag.Int("root", 0, "restrict view to process subtree")
	focus := flag.Int("focus", 0, "highlight a PID")
	palette := flag.String("palette", "cosmos", "color palette: cosmos, aurora, ember")
	trails := flag.Bool("trails", true, "show fading motion trails")
	orbit := flag.Bool("orbit", true, "add gentle orbital motion")
	noColor := flag.Bool("no-color", false, "disable ANSI colors")
	flag.Parse()
	if *interval < 30*time.Millisecond || *sample < 100*time.Millisecond || *maxNodes < 1 || *frames < 0 {
		fmt.Fprintln(os.Stderr, "invalid interval, sample, frames or max-nodes")
		os.Exit(2)
	}
	u := newUniverse()
	u.ShowNames = *names
	u.ShowLinks = *links
	u.MaxNodes = *maxNodes
	u.Filter = *filter
	u.Root = *root
	u.Focus = *focus
	u.Palette = *palette
	u.Trails = *trails
	u.Orbit = *orbit
	u.Color = !*noColor
	if u.Palette != "cosmos" && u.Palette != "aurora" && u.Palette != "ember" {
		fmt.Fprintln(os.Stderr, "invalid palette")
		os.Exit(2)
	}
	u.sample()
	if *once {
		printSummary(u)
		return
	}
	if *jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(u.visible())
		return
	}
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	defer fmt.Print("\x1b[?25h\x1b[0m\n")
	fmt.Print("\x1b[?25l\x1b[2J")
	animation := time.NewTicker(*interval)
	defer animation.Stop()
	sampling := time.NewTicker(*sample)
	defer sampling.Stop()
	for {
		select {
		case <-sig:
			return
		case <-sampling.C:
			u.sample()
		case <-animation.C:
			u.Frame++
			fmt.Print(u.render())
			if *frames > 0 && u.Frame >= *frames {
				return
			}
		}
	}
}
