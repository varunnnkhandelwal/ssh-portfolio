package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	bm "github.com/charmbracelet/wish/bubbletea"
	overlay "github.com/rmhubbert/bubbletea-overlay"
)

const (
	host         = "0.0.0.0"
	port         = "2323"
	fixedW       = 110 // locked UI width
	fixedH       = 38  // locked UI height
	textColWidth = 44
	pgInner      = 82  // playground overlay inner content width
	pgH          = 28  // playground overlay total height (incl. border)
)

// ── ASCII art constants ───────────────────────────────────────────────────────

const nameArt = `██╗   ██╗ █████╗ ██████╗ ██╗   ██╗███╗   ██╗
██║   ██║██╔══██╗██╔══██╗██║   ██║████╗  ██║
██║   ██║███████║██████╔╝██║   ██║██╔██╗ ██║
╚██╗ ██╔╝██╔══██║██╔══██╗██║   ██║██║╚██╗██║
 ╚████╔╝ ██║  ██║██║  ██║╚██████╔╝██║ ╚████║
  ╚═══╝  ╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═══╝`

const asciiArt = `xXXXXXxXXXXXXXXxxxXXXXxxXXxxXXXxxxxXXXxxxxxxxxxxxxxXX
XXXxxxxXXXXXXXXXxxxxx+:+xxxxxxxxxxxxxxxxxxxxxxxxxxxxx
xXXxxxxxxxxxXxx;;....  ...:xxxxxxxxxxxxxxxxxxxxxxxxxx
XXXxxxxxxxxxx:... ...    ....:xxxxxxxxxxxxxxxxxxxxxxx
xxxxxxxxXXx:.....    ..  ......;xxxxxxxxxxxxxxxxxxxxx
xxxxxxxxXx:.....    ............+xxxxxxxxxxxxxxxxxxxx
xXxxxxxxXx......  .        ....;xxxxxxxxxxxxxxxxxxxxx
xxxxxxXXXX..   .......... .....;xxx+xxx+xx+++xxxxxx++
xxxxxxXX$x.......::;:::....   +xx++xxxxxxx++++++++x+
xxxxxXX$$Xx.::;;++++xx+;;::...;xxxxxxxxxxxxxxxxxxxxx
xxxxXXX$XX;.:;;++xx+;::.::;;.:+xxxxxxxxxxxxx+xxxxxxx
.xxxxxXXXxxx:;;::::;;:.:::;;;:;+xxxxxxxxxxxxxxx+xxxxx
.xxxxxxXXXXxx+;;+;:x+;x+xx+;;:;+xxxxx+xxxxxxx+++xxxxx
.xxxxxxXXXXxx++++;;xx+;+xx+;:....:::::;;+++++++++xxxx
.xxxXXX$XXx;.;+++;;++;;+;+;;:......::.:::::;++++x++++
.:+xxxx+;.....:::::::;;:::;..........:...:::::;x+++++
.:+x;:..........;+:::::;;:................:::.:;++++;
;+;:............;xxxxx+++:.......::..........:..;+;;;
::::............;xxxxxx+++..................::...;;;;
;:..............;+++xxxxx++......................:;;;
x:.............:;+++++xxxx+:.....................:;;:
x;.::..........:;++++++++:.......................:;:.
+;:...........:;;;+++;;;:.........................::.
xx:...........:;;;+++;:..............................
x+;::........:;;;;+;.................................
+x;.::::.....::::;::.........  ...... ...............
;xx:........:::::::........ ......... ...............
xxx+::......:::::::..........         ...............
Xxx+::....::;;;::::........  ..        ..............
Xxx+:..:.::;;;;::::..................  ..............
xxx+;..:.:::;::;;::. ............................... 
xxxx::.::.::;::;;::................................. 
xxxx;:..:::::.::::.................................. 
xxxx++:............................:;;;;:..........`

// ── Palette ───────────────────────────────────────────────────────────────────

var (
	colBg     = lipgloss.Color("#0D1117")
	colBorder = lipgloss.Color("#30363D")
	colMuted  = lipgloss.Color("#C0C0C0")
	colSubtle = lipgloss.Color("#D8D8D8")
	colText   = lipgloss.Color("#FFFFFF")
	colAccent = lipgloss.Color("#FF6B00")
)

// ── Styles ────────────────────────────────────────────────────────────────────

type styles struct{ r *lipgloss.Renderer }

func newStyles(r *lipgloss.Renderer) styles { return styles{r: r} }

func (s styles) fg(c lipgloss.Color) lipgloss.Style   { return s.r.NewStyle().Foreground(c) }
func (s styles) bold(c lipgloss.Color) lipgloss.Style { return s.r.NewStyle().Bold(true).Foreground(c) }
func (s styles) muted() lipgloss.Style                { return s.r.NewStyle().Foreground(colMuted) }
func (s styles) text() lipgloss.Style                 { return s.r.NewStyle().Foreground(colText) }
func (s styles) italic(c lipgloss.Color) lipgloss.Style {
	return s.r.NewStyle().Italic(true).Foreground(c)
}

// ── Screen enum ───────────────────────────────────────────────────────────────

type screen int

const (
	screenHome screen = iota
	screenAbout
	screenProjects
	screenExperience
	screenContact
	screenPlayground
)

var menuItems = []struct {
	icon   string
	label  string
	target screen
}{
	{"▸ ", "About", screenAbout},
	{"▸ ", "Projects", screenProjects},
	{"▸ ", "Experience", screenExperience},
	{"▸ ", "Contact", screenContact},
	{"▸ ", "Playground", screenPlayground},
}

// ── Themes ────────────────────────────────────────────────────────────────────

var themes = []struct {
	name  string
	color lipgloss.Color
}{
	{"orange", lipgloss.Color("#FF6B00")},
	{"blue", lipgloss.Color("#00B4D8")},
	{"yellow", lipgloss.Color("#F0C040")},
	{"green", lipgloss.Color("#3FB950")},
}

// ── Playground ───────────────────────────────────────────────────────────────

type playground struct {
	ti        textinput.Model
	responses []string
	inGame    bool
	game      gameState
}

// ── Cheat codes ───────────────────────────────────────────────────────────────

type cheatStep int

const (
	cheatNone      cheatStep = iota
	cheatFlash               // 1: white flash
	cheatStars               // 2: wanted stars one-by-one
	cheatMoney               // 3: money counter
	cheatMatrix              // 4: matrix rain
	cheatResult              // 5: result screen
	cheatRocket              // ROCKETMAN: rocket rising
	cheatRocketDone          // ROCKETMAN: jetpack screen
)

type cheatTickMsg struct{}

func cheatTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return cheatTickMsg{} })
}

var matrixRunes = []rune("ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ@#$%&*!?<>|\\+=~")

func randomMatrixChar() rune {
	return matrixRunes[rand.Intn(len(matrixRunes))]
}

func newMatrixGrid(w, h int) ([][]rune, []int) {
	grid := make([][]rune, h)
	for r := range grid {
		grid[r] = make([]rune, w)
		for c := range grid[r] {
			grid[r][c] = randomMatrixChar()
		}
	}
	heads := make([]int, w)
	for c := range heads {
		heads[c] = rand.Intn(h)
	}
	return grid, heads
}

func advanceMatrix(grid [][]rune, heads []int) {
	rows := len(grid)
	for c := range heads {
		heads[c]++
		if heads[c] > rows+6 {
			heads[c] = -rand.Intn(6)
		}
		for r := range grid {
			if rand.Intn(3) == 0 {
				grid[r][c] = randomMatrixChar()
			}
		}
	}
}

var rocketLines = []string{
	`          *          `,
	`         /|\         `,
	`        / | \        `,
	`       /  |  \       `,
	`      /   |   \      `,
	`     |   [*]   |     `,
	`     |  [   ]  |     `,
	`     |_________|     `,
	`       |     |       `,
	`      /|     |\      `,
	`     / |     | \     `,
}

var rocketFlame1 = []string{
	`       * * * *       `,
	`      /\/\/\/\       `,
	`     /  \  /  \      `,
}

var rocketFlame2 = []string{
	`       * * * *       `,
	`      \/\/\/\/       `,
	`       \  /  \       `,
}

// ── Model ─────────────────────────────────────────────────────────────────────

type doneLoadingMsg struct{}
type typingTickMsg struct{}

var nameArtRunes = []rune(nameArt)

type model struct {
	st             styles
	sp             spinner.Model
	loading        bool
	screen         screen
	menuCursor     int
	themeIdx       int
	vp             viewport.Model
	vpReady        bool
	width          int
	height         int
	revealedChars  int
	typingDone     bool
	playgroundOpen  bool
	pg              playground
	projectList     list.Model
	contactList     list.Model
	// cheat animation
	cheatActive  bool
	cheatStep    cheatStep
	cheatSubStep int
	cheatStars   int
	cheatMoney   int
	cheatMatrix  [][]rune
	cheatMatrixH []int
	cheatRocketY int
}

func newPlayground(r *lipgloss.Renderer) playground {
	ti := textinput.New()
	ti.Placeholder = "type a command..."
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	ti.PromptStyle = r.NewStyle().Foreground(colAccent).Bold(true)
	ti.TextStyle = r.NewStyle().Foreground(colText)
	ti.Prompt = "> "
	ti.Width = pgInner - 4
	ti.Focus()
	return playground{ti: ti}
}

func newModel(w, h int, r *lipgloss.Renderer) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = r.NewStyle().Foreground(colAccent)

	pItems := make([]list.Item, len(projects))
	for i, p := range projects {
		pItems[i] = projectItem{p}
	}
	pl := list.New(pItems, projectDelegate{st: newStyles(r)}, fixedW, fixedH-4)
	pl.SetShowTitle(false)
	pl.SetShowFilter(false)
	pl.SetShowStatusBar(false)
	pl.SetShowHelp(false)
	pl.SetFilteringEnabled(false)
	pl.KeyMap.Quit.Unbind()

	cItems := make([]list.Item, len(contactItems))
	for i, c := range contactItems {
		cItems[i] = c
	}
	cl := list.New(cItems, contactDelegate{st: newStyles(r)}, fixedW, 18)
	cl.SetShowTitle(false)
	cl.SetShowFilter(false)
	cl.SetShowStatusBar(false)
	cl.SetShowHelp(false)
	cl.SetFilteringEnabled(false)
	cl.KeyMap.Quit.Unbind()

	return model{
		st:          newStyles(r),
		sp:          sp,
		loading:     true,
		screen:      screenHome,
		width:       w,
		height:      h,
		pg:          newPlayground(r),
		projectList: pl,
		contactList: cl,
	}
}

func waitDone() tea.Cmd {
	return func() tea.Msg {
		time.Sleep(5 * time.Second)
		return doneLoadingMsg{}
	}
}

func typingTick() tea.Cmd {
	return tea.Tick(18*time.Millisecond, func(time.Time) tea.Msg {
		return typingTickMsg{}
	})
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.sp.Tick, waitDone(), typingTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {

	case doneLoadingMsg:
		m.loading = false
		return m, nil

	case typingTickMsg:
		if !m.typingDone {
			m.revealedChars++
			if m.revealedChars >= len(nameArtRunes) {
				m.typingDone = true
			} else {
				return m, typingTick()
			}
		}
		return m, nil

	case spinner.TickMsg:
		m.sp, cmd = m.sp.Update(msg)
		return m, cmd

	case cheatTickMsg:
		if m.cheatActive {
			return m.handleCheatTick()
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.vpReady {
			m.vp = viewport.New(fixedW, fixedH-4)
			m.vpReady = true
		}
		m.projectList.SetSize(fixedW, fixedH-4)

	case tea.KeyMsg:
		if msg.String() == "ctrl+t" && !m.loading {
			m.themeIdx = (m.themeIdx + 1) % len(themes)
			colAccent = themes[m.themeIdx].color
			if m.screen != screenHome {
				m.vp.SetContent(m.pageContent())
			}
			return m, nil
		}
		if m.loading {
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}

		// ── Cheat animation key routing ───────────────────────────────────────
		if m.cheatActive {
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			if m.cheatStep == cheatResult || m.cheatStep == cheatRocketDone {
				m.cheatActive = false
				m.screen = screenHome
			}
			return m, nil
		}

		// ── Playground overlay key routing ────────────────────────────────────
		if m.playgroundOpen {
			if m.pg.inGame {
				switch msg.String() {
				case "q", "ctrl+c":
					return m, tea.Quit
				case "esc":
					m.pg.inGame = false
				case "r":
					oldBest := m.pg.game.best
					if m.pg.game.score > oldBest {
						oldBest = m.pg.game.score
					}
					m.pg.game = newGame()
					m.pg.game.best = oldBest
				case "up", "k", "w":
					if !m.pg.game.over {
						if m.pg.game.moveUp() {
							m.pg.game.spawnTile()
						}
						m.pg.game.afterMove()
					}
				case "down", "j", "s":
					if !m.pg.game.over {
						if m.pg.game.moveDown() {
							m.pg.game.spawnTile()
						}
						m.pg.game.afterMove()
					}
				case "left", "h", "a":
					if !m.pg.game.over {
						if m.pg.game.moveLeft() {
							m.pg.game.spawnTile()
						}
						m.pg.game.afterMove()
					}
				case "right", "l", "d":
					if !m.pg.game.over {
						if m.pg.game.moveRight() {
							m.pg.game.spawnTile()
						}
						m.pg.game.afterMove()
					}
				}
			} else {
				switch msg.String() {
				case "q", "ctrl+c":
					return m, tea.Quit
				case "esc":
					m.playgroundOpen = false
				case "enter":
					input := strings.TrimSpace(m.pg.ti.Value())
					if input != "" {
						resp := m.handleCommand(input)
						m.pg.ti.SetValue("")
						switch resp {
						case "_NAV_CONTACT_":
							m.playgroundOpen = false
							m.screen = screenContact
							m.vp.GotoTop()
							m.vp.SetContent(m.pageContent())
						case "_GAME_":
							m.pg.game = newGame()
							m.pg.inGame = true
						case "_CHEAT_HESOYAM_":
							m.playgroundOpen = false
							m.cheatActive = true
							m.cheatStep = cheatFlash
							m.cheatSubStep = 0
							m.cheatStars = 0
							m.cheatMoney = 0
							return m, cheatTick(50 * time.Millisecond)
						case "_CHEAT_ROCKETMAN_":
							m.playgroundOpen = false
							m.cheatActive = true
							m.cheatStep = cheatRocket
							m.cheatSubStep = 0
							m.cheatRocketY = fixedH - len(rocketLines) - len(rocketFlame1)
							return m, cheatTick(60 * time.Millisecond)
						case "_CLEAR_":
							m.pg.responses = nil
						default:
							m.pg.responses = append(m.pg.responses, "> "+input+"\n"+resp)
							if len(m.pg.responses) > 5 {
								m.pg.responses = m.pg.responses[1:]
							}
						}
					}
				default:
					m.pg.ti, cmd = m.pg.ti.Update(msg)
					return m, cmd
				}
			}
			return m, nil
		}

		// ── Normal screen routing ─────────────────────────────────────────────
		switch m.screen {
		case screenHome:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "up":
				if m.menuCursor > 0 {
					m.menuCursor--
				}
			case "down":
				if m.menuCursor < len(menuItems)-1 {
					m.menuCursor++
				}
			case "enter":
				target := menuItems[m.menuCursor].target
				if target == screenPlayground {
					m.playgroundOpen = true
				} else {
					m.screen = target
					m.vp.GotoTop()
					m.vp.SetContent(m.pageContent())
				}
			}

		case screenProjects:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.screen = screenHome
			case "enter":
				if item, ok := m.projectList.SelectedItem().(projectItem); ok && item.url != "" {
					return m, openURL(item.url)
				}
			default:
				m.projectList, cmd = m.projectList.Update(msg)
				return m, cmd
			}

		case screenContact:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.screen = screenHome
			case "enter":
				if item, ok := m.contactList.SelectedItem().(contactItem); ok && item.url != "" {
					return m, openURL(item.url)
				}
			default:
				m.contactList, cmd = m.contactList.Update(msg)
				return m, cmd
			}

		default: // content screens
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.screen = screenHome
			default:
				m.vp, cmd = m.vp.Update(msg)
			}
		}
	}

	if m.screen != screenHome {
		m.vp, cmd = m.vp.Update(msg)
	}

	return m, cmd
}

// ── View dispatcher ───────────────────────────────────────────────────────────

func (m model) place(content string) string {
	box := m.st.r.NewStyle().
		Width(fixedW).
		Height(fixedH).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m model) View() string {
	if m.loading {
		return m.place(m.viewLoading())
	}
	if m.cheatActive {
		switch m.cheatStep {
		case cheatFlash:
			return m.viewCheatFlash()
		case cheatMatrix:
			return m.viewCheatMatrix()
		default:
			return m.place(m.viewCheatContent())
		}
	}
	bg := m.place(m.viewHome())
	if m.screen == screenProjects {
		bg = m.place(m.viewProjectList())
	} else if m.screen == screenContact {
		bg = m.place(m.viewContactList())
	} else if m.screen != screenHome {
		bg = m.place(m.viewContent())
	}
	if m.playgroundOpen {
		fg := m.renderPlayground()
		return overlay.Composite(fg, bg, overlay.Center, overlay.Center, 0, 0)
	}
	return bg
}

// ── Project list view ────────────────────────────────────────────────────────

func (m model) viewProjectList() string {
	st := m.st
	topBar := st.bold(colAccent).Render("Projects") +
		"  " + st.muted().Render("·") +
		"  " + st.muted().Render("esc to go back")
	divider := st.fg(colBorder).Render(strings.Repeat("─", fixedW))
	hint := st.italic(colMuted).Render("↑↓ navigate  ·  enter open  ·  esc back  ·  q quit")
	return topBar + "\n" + divider + "\n" + m.projectList.View() + "\n" + divider + "\n" + hint
}

// ── Loading screen ────────────────────────────────────────────────────────────

func (m model) viewLoading() string {
	st := m.st

	n := m.revealedChars
	if n > len(nameArtRunes) {
		n = len(nameArtRunes)
	}
	revealed := string(nameArtRunes[:n])

	var b strings.Builder
	for _, line := range strings.Split(revealed, "\n") {
		b.WriteString(st.fg(colAccent).Render(line) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(m.sp.View() + st.muted().Render(" Loading portfolio..."))
	return lipgloss.NewStyle().Width(fixedW).Align(lipgloss.Center).Render(b.String())
}

// ── Home screen ───────────────────────────────────────────────────────────────

func (m model) viewHome() string {
	st := m.st
	var b strings.Builder

	for _, line := range strings.Split(nameArt, "\n") {
		b.WriteString(st.fg(colAccent).Render(line) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(st.muted().Render("Product Designer · Bajaj Finserv Health · Pune, India") + "\n\n\n")

	for i, item := range menuItems {
		if i == m.menuCursor {
			b.WriteString(st.bold(colAccent).Render("  › "+item.label) + "\n\n")
		} else {
			b.WriteString(st.muted().Render("    "+item.label) + "\n\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(st.italic(colMuted).Render("↑↓ navigate  ·  enter select  ·  ctrl+t theme  ·  q quit"))

	return lipgloss.NewStyle().Width(fixedW).Align(lipgloss.Center).Render(b.String())
}

// ── Command handler ───────────────────────────────────────────────────────────

func (m model) handleCommand(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "help":
		return "  help · hello · hire · synthwave · emr · whoami · contact · 2048 · clear"
	case "hello", "hi":
		return "  Hey! Good to have you here."
	case "hire", "hire me":
		return "  Currently open to interesting opportunities. Check my resume or reach out directly."
	case "synthwave":
		return "  A vibe-coded music creation platform. Three unique ways to make sounds in the browser. Built for fun."
	case "emr":
		return "  Generative EMR — redesigning how doctors document patient care using AI. In active development at Bajaj Finserv Health."
	case "whoami":
		return "  Product designer at Bajaj Finserv Health. I make health tech less ugly and AI more usable."
	case "contact":
		return "_NAV_CONTACT_"
	case "2048", "game":
		return "_GAME_"
	case "hesoyam":
		return "_CHEAT_HESOYAM_"
	case "rocketman":
		return "_CHEAT_ROCKETMAN_"
	case "clear":
		return "_CLEAR_"
	default:
		return fmt.Sprintf("  command not found: %s. try 'help'", input)
	}
}

// ── Content screen wrapper ────────────────────────────────────────────────────

func (m model) viewContent() string {
	if !m.vpReady {
		return ""
	}

	st := m.st

	title := map[screen]string{
		screenAbout:      "About",
		screenProjects:   "Projects",
		screenExperience: "Experience",
		screenContact:    "Contact",
	}[m.screen]

	topBar := st.bold(colAccent).Render(title) +
		"  " + st.muted().Render("·") +
		"  " + st.muted().Render("esc to go back")

	divider := st.fg(colBorder).Render(strings.Repeat("─", fixedW))

	pct := st.muted().Render(fmt.Sprintf("%d%%", int(m.vp.ScrollPercent()*100)))
	hint := st.italic(colMuted).Render("j/k scroll  ·  esc back  ·  ctrl+t theme  ·  q quit")
	gap := fixedW - lipgloss.Width(hint) - lipgloss.Width(pct)
	if gap < 1 {
		gap = 1
	}
	footer := hint + strings.Repeat(" ", gap) + pct

	return topBar + "\n" + divider + "\n" + m.vp.View() + "\n" + divider + "\n" + footer
}

// ── Page content ──────────────────────────────────────────────────────────────

func (m model) pageContent() string {
	switch m.screen {
	case screenAbout:
		return m.renderAbout()
	case screenExperience:
		return m.renderExperience()
}
	return ""
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func (m model) heading(title string) string {
	return m.st.bold(colAccent).Render(title)
}

func (m model) tagRow(tags []string) string {
	parts := make([]string, 0, len(tags)*2)
	for i, t := range tags {
		parts = append(parts, m.st.r.NewStyle().
			Foreground(colBg).
			Background(colSubtle).
			Padding(0, 1).
			Render(t))
		if i < len(tags)-1 {
			parts = append(parts, " ")
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// ── About ─────────────────────────────────────────────────────────────────────

func (m model) renderAbout() string {
	st := m.st
	leftW := textColWidth // 44 chars; art is ~55 wide → total ~101, fits in 110

	var left strings.Builder
	left.WriteString("\n\n")
	left.WriteString(st.text().Render(wrapText(
		"Product designer with 2 years of experience in Health Tech, AI, and Visual Design. Currently working as Associate Product Designer at Bajaj Finserv Health. I design things that are both aesthetically pleasing and actually buildable.",
		leftW,
	)) + "\n\n\n")
	left.WriteString(m.heading("What I'm working on") + "\n\n")
	for _, item := range []string{
		"Generative EMR — reimagining medical records with AI",
		"Smart Health Mirror — spatial ambient health tracking",
		"Synthwave — vibe-coded music creation platform",
		"Sign Buddy — inclusive design for the hearing impaired",
	} {
		left.WriteString(st.fg(colAccent).Render("→") + "  " + st.text().Render(item) + "\n\n")
	}
	left.WriteString("\n")
	left.WriteString(m.heading("Skills") + "\n\n")
	left.WriteString(m.tagRow([]string{"UX/UI Design", "Figma", "Product Thinking", "AI Prompting", "Web Design", "Illustration", "Vibe Coding"}) + "\n")

	// Right: ASCII art
	artLines := strings.Split(asciiArt, "\n")
	rendered := make([]string, len(artLines))
	for i, line := range artLines {
		rendered[i] = st.fg(colText).Render(line)
	}
	art := strings.Join(rendered, "\n")

	artWidth := 0
	for _, l := range artLines {
		if len(l) > artWidth {
			artWidth = len(l)
		}
	}
	gapW := fixedW - leftW - artWidth
	if gapW < 1 {
		gapW = 1
	}

	leftBlock := st.r.NewStyle().Width(leftW).Render(left.String())
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, strings.Repeat(" ", gapW), art)
	return body
}
// ── Projects ──────────────────────────────────────────────────────────────────

type projectItem struct{ project }

func (p projectItem) Title() string       { return p.name }
func (p projectItem) Description() string { return p.status }
func (p projectItem) FilterValue() string { return p.name }

type projectDelegate struct{ st styles }

func (d projectDelegate) Height() int                             { return 4 }
func (d projectDelegate) Spacing() int                            { return 1 }
func (d projectDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d projectDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	p, ok := item.(projectItem)
	if !ok {
		return
	}
	sel := index == m.Index()
	var sb strings.Builder
	// description wrapped to 2 lines
	wrapped := strings.SplitN(wrapText(p.detail, fixedW-6), "\n", 3)
	for len(wrapped) < 2 {
		wrapped = append(wrapped, "")
	}
	if sel {
		borderSt := d.st.r.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colAccent).
			PaddingLeft(1)
		sb.WriteString(borderSt.Bold(true).Foreground(colAccent).Render(p.name) + "\n")
		sb.WriteString(borderSt.Foreground(colMuted).Bold(false).Render(p.status) + "\n")
		for _, l := range wrapped[:2] {
			sb.WriteString(borderSt.Foreground(colSubtle).Bold(false).Render(l) + "\n")
		}
	} else {
		normSt := d.st.r.NewStyle().PaddingLeft(3)
		sb.WriteString(normSt.Foreground(colText).Bold(false).Render(p.name) + "\n")
		sb.WriteString(normSt.Foreground(lipgloss.Color("#666666")).Render(p.status) + "\n")
		for _, l := range wrapped[:2] {
			sb.WriteString(normSt.Foreground(lipgloss.Color("#555555")).Render(l) + "\n")
		}
	}
	fmt.Fprint(w, sb.String())
}

type project struct {
	name, status, detail, url string
	tags                      []string
}

var projects = []project{
	{
		name:   "Generative EMR",
		status: "In Development · 2025",
		detail: "Reimagining electronic medical records using generative AI — smarter documentation, faster workflows, better care.",
		url:    "https://varunnkhandelwal.framer.website/emr",
		tags:   []string{"Generative AI", "Health Tech"},
	},
	{
		name:   "Smart Health Mirror",
		status: "Concept · 2025",
		detail: "An ambient health tracking mirror that surfaces vitals and wellness data in your physical space using spatial design principles.",
		url:    "https://varunnkhandelwal.framer.website/smart-mirror",
		tags:   []string{"Spatial Design", "Concept"},
	},
	{
		name:   "LV x Cred",
		status: "Concept · 2023",
		detail: "A visual design concept exploring a collaboration between Louis Vuitton and CRED — brand identity and campaign design.",
		url:    "https://varunnkhandelwal.framer.website/lvxcred",
		tags:   []string{"Visual Design", "Concept"},
	},
}


// ── Contact ───────────────────────────────────────────────────────────────────

type contactItem struct {
	label, display, url string
}

func (c contactItem) Title() string       { return c.label }
func (c contactItem) Description() string { return c.display }
func (c contactItem) FilterValue() string { return c.label }

type contactDelegate struct{ st styles }

func (d contactDelegate) Height() int                             { return 2 }
func (d contactDelegate) Spacing() int                            { return 1 }
func (d contactDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d contactDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	c, ok := item.(contactItem)
	if !ok {
		return
	}
	sel := index == m.Index()
	var sb strings.Builder
	if sel {
		borderSt := d.st.r.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colAccent).
			PaddingLeft(1)
		sb.WriteString(borderSt.Bold(true).Foreground(colAccent).Render(c.label) + "\n")
		if c.url != "" {
			linkStr := osc8Link(c.url, d.st.r.NewStyle().Foreground(lipgloss.Color("#00B4D8")).Underline(true).Render(c.display))
			sb.WriteString(borderSt.Bold(false).Render(linkStr) + "\n")
		} else {
			sb.WriteString(borderSt.Foreground(colSubtle).Bold(false).Render(c.display) + "\n")
		}
	} else {
		normSt := d.st.r.NewStyle().PaddingLeft(3)
		sb.WriteString(normSt.Foreground(colText).Render(c.label) + "\n")
		sb.WriteString(normSt.Foreground(lipgloss.Color("#555555")).Render(c.display) + "\n")
	}
	fmt.Fprint(w, sb.String())
}

var contactItems = []contactItem{
	{"Resume", "drive.google.com/file/d/1vy7ce_YFVtPCPFApFfFff-ny_IVre1UM/view", "https://drive.google.com/file/d/1vy7ce_YFVtPCPFApFfFff-ny_IVre1UM/view"},
	{"Website", "varunnkhandelwal.framer.website", "https://varunnkhandelwal.framer.website"},
	{"GitHub", "github.com/varunnnkhandelwal", "https://github.com/varunnnkhandelwal"},
	{"LinkedIn", "linkedin.com/in/varun-khandelwal-200062261", "https://www.linkedin.com/in/varun-khandelwal-200062261"},
	{"Instagram", "instagram.com/varunnkhandelwal", "https://www.instagram.com/varunnkhandelwal/?hl=en"},
}

func (m model) viewContactList() string {
	st := m.st
	topBar := st.bold(colAccent).Render("Contact") +
		"  " + st.muted().Render("·") +
		"  " + st.muted().Render("esc to go back")
	divider := st.fg(colBorder).Render(strings.Repeat("─", fixedW))

	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(st.text().Render(wrapText(
		"Always happy to chat about product, design, AI, or anything interesting.",
		fixedW-4,
	)) + "\n\n\n")
	b.WriteString(m.contactList.View())
	b.WriteString("\n\n")
	b.WriteString(st.muted().Width(12).Render("Location") + st.text().Render("Pune, India  (IST, UTC+5:30)"))

	hint := st.italic(colMuted).Render("↑↓ navigate  ·  enter open  ·  esc back  ·  q quit")
	return topBar + "\n" + divider + "\n" + b.String() + "\n" + divider + "\n" + hint
}

// ── Experience ────────────────────────────────────────────────────────────────

type role struct {
	title, org, period string
}

func (m model) renderExperience() string {
	st := m.st
	tw := fixedW - 6
	var b strings.Builder
	b.WriteString("\n")

	b.WriteString(m.heading("Work") + "\n\n")
	for i, r := range []role{
		{"Associate Product Designer", "Bajaj Finserv Health", "2025"},
		{"Design Intern", "Bajaj Finserv Health", "2024"},
		{"UX Design Intern", "Varpas Concepts", "2024"},
		{"Freelance Designer", "Self Employed", "2021 – Present"},
	} {
		titleStr := st.bold(colAccent).Render(r.title)
		orgStr := st.text().Render(r.org)
		periodStr := st.muted().Render(r.period)
		gap := tw - lipgloss.Width(r.title) - lipgloss.Width(r.org) - lipgloss.Width(r.period)
		if gap < 1 {
			gap = 1
		}
		b.WriteString(titleStr + "  " + orgStr + strings.Repeat(" ", gap) + periodStr + "\n\n")
		if i < 3 {
			b.WriteString(st.fg(colBorder).Render(strings.Repeat("─", tw)) + "\n\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(m.heading("Education") + "\n\n")
	title := st.bold(colAccent).Render("Bachelor's in Design")
	school := st.text().Render("MIT Institute of Design")
	period := st.muted().Render("2021 – 2025")
	gap := tw - lipgloss.Width("Bachelor's in Design") - lipgloss.Width("MIT Institute of Design") - lipgloss.Width("2021 – 2025")
	if gap < 1 {
		gap = 1
	}
	b.WriteString(title + "  " + school + strings.Repeat(" ", gap) + period + "\n")
	return b.String()
}

// ── Text wrap ─────────────────────────────────────────────────────────────────

func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	words := strings.Fields(text)
	var lines []string
	var line strings.Builder
	for _, w := range words {
		if line.Len() == 0 {
			line.WriteString(w)
		} else if line.Len()+1+len(w) <= width {
			line.WriteString(" " + w)
		} else {
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(w)
		}
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return strings.Join(lines, "\n")
}

// ── 2048 game ─────────────────────────────────────────────────────────────────

type gameState struct {
	board [4][4]int
	score int
	best  int
	over  bool
	won   bool
}

func newGame() gameState {
	g := gameState{}
	g.spawnTile()
	g.spawnTile()
	return g
}

func (g *gameState) spawnTile() {
	var empty [][2]int
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			if g.board[r][c] == 0 {
				empty = append(empty, [2]int{r, c})
			}
		}
	}
	if len(empty) == 0 {
		return
	}
	pos := empty[rand.Intn(len(empty))]
	if rand.Intn(10) < 1 {
		g.board[pos[0]][pos[1]] = 4
	} else {
		g.board[pos[0]][pos[1]] = 2
	}
}

func mergeRow(row [4]int) ([4]int, int) {
	var compact [4]int
	ci := 0
	for _, v := range row {
		if v != 0 {
			compact[ci] = v
			ci++
		}
	}
	score := 0
	for i := 0; i < 3; i++ {
		if compact[i] != 0 && compact[i] == compact[i+1] {
			compact[i] *= 2
			score += compact[i]
			compact[i+1] = 0
		}
	}
	var result [4]int
	ri := 0
	for _, v := range compact {
		if v != 0 {
			result[ri] = v
			ri++
		}
	}
	return result, score
}

func (g *gameState) moveLeft() bool {
	moved := false
	for r := 0; r < 4; r++ {
		newRow, s := mergeRow(g.board[r])
		g.score += s
		if newRow != g.board[r] {
			moved = true
		}
		g.board[r] = newRow
	}
	return moved
}

func (g *gameState) moveRight() bool {
	moved := false
	for r := 0; r < 4; r++ {
		rev := [4]int{g.board[r][3], g.board[r][2], g.board[r][1], g.board[r][0]}
		newRow, s := mergeRow(rev)
		g.score += s
		unrev := [4]int{newRow[3], newRow[2], newRow[1], newRow[0]}
		if unrev != g.board[r] {
			moved = true
		}
		g.board[r] = unrev
	}
	return moved
}

func (g *gameState) transpose() {
	for r := 0; r < 4; r++ {
		for c := r + 1; c < 4; c++ {
			g.board[r][c], g.board[c][r] = g.board[c][r], g.board[r][c]
		}
	}
}

func (g *gameState) moveUp() bool {
	g.transpose()
	moved := g.moveLeft()
	g.transpose()
	return moved
}

func (g *gameState) moveDown() bool {
	g.transpose()
	moved := g.moveRight()
	g.transpose()
	return moved
}

func (g *gameState) checkWin() bool {
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			if g.board[r][c] == 2048 {
				return true
			}
		}
	}
	return false
}

func (g *gameState) checkOver() bool {
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			if g.board[r][c] == 0 {
				return false
			}
			if c < 3 && g.board[r][c] == g.board[r][c+1] {
				return false
			}
			if r < 3 && g.board[r][c] == g.board[r+1][c] {
				return false
			}
		}
	}
	return true
}

func (g *gameState) afterMove() {
	if g.score > g.best {
		g.best = g.score
	}
	if !g.won && g.checkWin() {
		g.won = true
	}
	if g.checkOver() {
		g.over = true
	}
}

func tileSt(st styles, v int) lipgloss.Style {
	type tt struct{ bg, fg lipgloss.Color }
	palette := map[int]tt{
		0:    {"#1E2329", "#444444"},
		2:    {"#3D3226", "#F5F5F5"},
		4:    {"#5C3D1A", "#F5F5F5"},
		8:    {"#A63500", "#FFFFFF"},
		16:   {"#C94400", "#FFFFFF"},
		32:   {"#E05500", "#FFFFFF"},
		64:   {"#D42200", "#FFFFFF"},
		128:  {"#C4830A", "#FFFFFF"},
		256:  {"#D49500", "#FFFFFF"},
		512:  {"#D4A800", "#FFFFFF"},
		1024: {"#2D8A4E", "#FFFFFF"},
		2048: {"#F0C000", "#1A1A1A"},
	}
	t, ok := palette[v]
	if !ok {
		t = tt{"#9B59B6", "#FFFFFF"}
	}
	return st.r.NewStyle().
		Width(9).
		Background(t.bg).
		Foreground(t.fg).
		Bold(true).
		Align(lipgloss.Center)
}


// ── Playground overlay ────────────────────────────────────────────────────────

func (m model) renderPlayground() string {
	st := m.st
	pg := m.pg

	var inner strings.Builder

	if pg.inGame {
		// ── Game mode ─────────────────────────────────────────────────────────
		g := pg.game
		var rows []string
		for r := 0; r < 4; r++ {
			var topP, midP, botP []string
			for c := 0; c < 4; c++ {
				v := g.board[r][c]
				ts := tileSt(st, v)
				numStr := ""
				if v != 0 {
					numStr = fmt.Sprintf("%d", v)
				}
				if c > 0 {
					topP = append(topP, " ")
					midP = append(midP, " ")
					botP = append(botP, " ")
				}
				topP = append(topP, ts.Render(" "))
				midP = append(midP, ts.Render(numStr))
				botP = append(botP, ts.Render(" "))
			}
			rows = append(rows,
				lipgloss.JoinHorizontal(lipgloss.Top, topP...),
				lipgloss.JoinHorizontal(lipgloss.Top, midP...),
				lipgloss.JoinHorizontal(lipgloss.Top, botP...),
			)
			if r < 3 {
				rows = append(rows, "")
			}
		}
		gridStr := strings.Join(rows, "\n")
		scoreLine := st.bold(colAccent).Render("SCORE ") + st.text().Render(fmt.Sprintf("%d", g.score)) +
			"   " + st.muted().Render("BEST ") + st.text().Render(fmt.Sprintf("%d", g.best))

		inner.WriteString(st.bold(colAccent).Render("2048") + "\n\n")
		inner.WriteString(scoreLine + "\n\n")
		inner.WriteString(gridStr + "\n\n")
		if g.won && !g.over {
			inner.WriteString(st.bold(lipgloss.Color("#F0C000")).Render("You reached 2048!  Keep going or press r.") + "\n\n")
		} else if g.over {
			inner.WriteString(st.bold(lipgloss.Color("#E05500")).Render("Game over!  Press r to restart.") + "\n\n")
		} else {
			inner.WriteString("\n")
		}
		inner.WriteString(st.italic(colMuted).Render("arrows/wasd move  \xc2\xb7  r restart  \xc2\xb7  esc back"))
	} else {
		// ── Command mode ──────────────────────────────────────────────────────
		for _, r := range pg.responses {
			lines := strings.SplitN(r, "\n", 2)
			if len(lines) == 2 {
				inner.WriteString(st.muted().Render(lines[0]) + "\n")
				inner.WriteString(st.text().Render(lines[1]) + "\n\n")
			}
		}
		if len(pg.responses) == 0 {
			inner.WriteString(st.muted().Render("Try: help  whoami  emr  synthwave  2048") + "\n\n")
		}
		inner.WriteString(pg.ti.View())
		inner.WriteString("\n\n")
		inner.WriteString(st.italic(colMuted).Render("esc close"))
	}

	panel := st.r.NewStyle().
		Width(pgInner).
		Height(pgH-2).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colAccent).
		Background(lipgloss.Color("#0D1117")).
		Render(inner.String())

	return panel
}

// ── Cheat animation logic ─────────────────────────────────────────────────────

func (m model) handleCheatTick() (tea.Model, tea.Cmd) {
	switch m.cheatStep {

	case cheatFlash:
		m.cheatStep = cheatStars
		m.cheatStars = 1
		return m, cheatTick(200 * time.Millisecond)

	case cheatStars:
		m.cheatStars++
		if m.cheatStars > 5 {
			m.cheatStep = cheatMoney
			m.cheatMoney = 0
			return m, cheatTick(120 * time.Millisecond)
		}
		return m, cheatTick(200 * time.Millisecond)

	case cheatMoney:
		if m.cheatMoney < 250000 {
			m.cheatMoney += 50000
			if m.cheatMoney >= 250000 {
				return m, cheatTick(350 * time.Millisecond)
			}
			return m, cheatTick(100 * time.Millisecond)
		}
		m.cheatStep = cheatMatrix
		m.cheatSubStep = 0
		m.cheatMatrix, m.cheatMatrixH = newMatrixGrid(m.width, m.height)
		return m, cheatTick(80 * time.Millisecond)

	case cheatMatrix:
		m.cheatSubStep++
		advanceMatrix(m.cheatMatrix, m.cheatMatrixH)
		if m.cheatSubStep >= 25 {
			m.cheatStep = cheatResult
			return m, nil
		}
		return m, cheatTick(80 * time.Millisecond)

	case cheatRocket:
		m.cheatSubStep++
		totalFrames := 20
		rocketH := len(rocketLines) + len(rocketFlame1)
		startY := fixedH - rocketH
		endY := 3
		progress := m.cheatSubStep * (startY - endY) / totalFrames
		m.cheatRocketY = startY - progress
		if m.cheatRocketY < endY {
			m.cheatRocketY = endY
		}
		if m.cheatSubStep >= totalFrames {
			m.cheatStep = cheatRocketDone
			return m, nil
		}
		return m, cheatTick(60 * time.Millisecond)
	}

	return m, nil
}

// ── Cheat views ───────────────────────────────────────────────────────────────

func (m model) viewCheatFlash() string {
	line := strings.Repeat(" ", m.width)
	var sb strings.Builder
	for i := 0; i < m.height; i++ {
		sb.WriteString("\x1b[47m\x1b[30m" + line + "\x1b[0m")
		if i < m.height-1 {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}

func (m model) viewCheatMatrix() string {
	if len(m.cheatMatrix) == 0 {
		return ""
	}
	rows := len(m.cheatMatrix)
	cols := len(m.cheatMatrixH)
	var sb strings.Builder
	for r := 0; r < rows; r++ {
		row := m.cheatMatrix[r]
		for c := 0; c < cols; c++ {
			var ch rune = ' '
			if c < len(row) {
				ch = row[c]
			}
			headR := -999
			if c < len(m.cheatMatrixH) {
				headR = m.cheatMatrixH[c]
			}
			dist := headR - r
			switch {
			case r == headR:
				sb.WriteString("\x1b[1;97m")
				sb.WriteRune(ch)
				sb.WriteString("\x1b[0m")
			case dist > 0 && dist <= 3:
				sb.WriteString("\x1b[1;32m")
				sb.WriteRune(ch)
				sb.WriteString("\x1b[0m")
			case dist > 3 && dist <= 8:
				sb.WriteString("\x1b[32m")
				sb.WriteRune(ch)
				sb.WriteString("\x1b[0m")
			case dist > 8:
				sb.WriteString("\x1b[2;32m")
				sb.WriteRune(ch)
				sb.WriteString("\x1b[0m")
			default:
				sb.WriteRune(' ')
			}
		}
		if r < rows-1 {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}

func (m model) viewCheatContent() string {
	switch m.cheatStep {
	case cheatStars, cheatMoney:
		return m.viewCheatWanted()
	case cheatResult:
		return m.viewCheatResult()
	case cheatRocket, cheatRocketDone:
		return m.viewCheatRocket()
	}
	return ""
}

func (m model) viewCheatWanted() string {
	st := m.st
	red := lipgloss.Color("#FF2222")
	green := lipgloss.Color("#00FF66")

	starParts := make([]string, m.cheatStars)
	for i := range starParts {
		starParts[i] = st.r.NewStyle().Foreground(red).Bold(true).Render("★")
	}
	starLine := strings.Join(starParts, "  ")

	var b strings.Builder
	b.WriteString("\n\n\n\n\n\n\n\n")
	b.WriteString(st.r.NewStyle().Width(fixedW).Align(lipgloss.Center).Render(starLine) + "\n\n")
	b.WriteString(st.r.NewStyle().Width(fixedW).Align(lipgloss.Center).Foreground(red).Bold(true).Render("WANTED LEVEL: 5") + "\n\n\n")

	if m.cheatStep == cheatMoney {
		moneyStr := fmt.Sprintf("$ %s", formatMoney(m.cheatMoney))
		b.WriteString(st.r.NewStyle().Width(fixedW).Align(lipgloss.Center).Foreground(green).Bold(true).Render(moneyStr) + "\n")
	}

	return b.String()
}

func formatMoney(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func (m model) viewCheatResult() string {
	st := m.st
	cyan := lipgloss.Color("#00FFFF")
	green := lipgloss.Color("#00FF66")
	center := st.r.NewStyle().Width(fixedW).Align(lipgloss.Center)

	var b strings.Builder
	b.WriteString("\n\n\n\n\n\n")
	b.WriteString(center.Foreground(cyan).Bold(true).Render("CHEAT CODE ACTIVATED") + "\n\n\n")
	for _, item := range []string{
		"▸ Health restored        ✓",
		"▸ Armor restored         ✓",
		"▸ $250,000 added         ✓",
	} {
		b.WriteString(center.Foreground(green).Render(item) + "\n")
	}
	b.WriteString("\n\n")
	b.WriteString(center.Foreground(lipgloss.Color("#888888")).Italic(true).Render("not bad for a terminal portfolio.") + "\n\n\n")
	b.WriteString(center.Foreground(lipgloss.Color("#555555")).Render("[ press any key to continue ]") + "\n")
	return b.String()
}

func (m model) viewCheatRocket() string {
	st := m.st
	cyan := lipgloss.Color("#00FFFF")
	orange := lipgloss.Color("#FF8800")
	yellow := lipgloss.Color("#FFDD00")
	center := st.r.NewStyle().Width(fixedW).Align(lipgloss.Center)
	rocketSt := st.r.NewStyle().Width(fixedW).Align(lipgloss.Center).Foreground(lipgloss.Color("#DDDDDD"))
	flameSt1 := st.r.NewStyle().Width(fixedW).Align(lipgloss.Center).Foreground(orange)
	flameSt2 := st.r.NewStyle().Width(fixedW).Align(lipgloss.Center).Foreground(yellow)

	if m.cheatStep == cheatRocketDone {
		var b strings.Builder
		b.WriteString("\n\n\n")
		for _, line := range rocketLines {
			b.WriteString(rocketSt.Render(line) + "\n")
		}
		b.WriteString("\n")
		b.WriteString(center.Foreground(cyan).Bold(true).Render("JETPACK ACQUIRED") + "\n\n")
		b.WriteString(center.Foreground(lipgloss.Color("#888888")).Italic(true).Render("the skies are yours.") + "\n\n\n")
		b.WriteString(center.Foreground(lipgloss.Color("#555555")).Render("[ press any key to continue ]") + "\n")
		return b.String()
	}

	var b strings.Builder
	y := m.cheatRocketY
	if y < 0 {
		y = 0
	}
	for i := 0; i < y; i++ {
		b.WriteString("\n")
	}
	for _, line := range rocketLines {
		b.WriteString(rocketSt.Render(line) + "\n")
	}
	flames := rocketFlame1
	if m.cheatSubStep%2 == 0 {
		flames = rocketFlame2
	}
	for i, line := range flames {
		fst := flameSt1
		if i == len(flames)-1 {
			fst = flameSt2
		}
		b.WriteString(fst.Render(line) + "\n")
	}
	return b.String()
}

// ── URL opener ───────────────────────────────────────────────────────────────

func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		exec.Command("open", url).Start()
		return nil
	}
}

// ── Hyperlink helper ─────────────────────────────────────────────────────────

// osc8Link wraps styled text with an OSC 8 terminal hyperlink.
func osc8Link(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// ── Wish server ───────────────────────────────────────────────────────────────

func main() {
	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		TimeFormat:      time.Kitchen,
	})

	srv, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath(".ssh/id_ed25519"),
		wish.WithMiddleware(
			bm.Middleware(teaHandler),
			activeterm.Middleware(),
		),
	)
	if err != nil {
		logger.Error("Could not create server", "error", err)
		os.Exit(1)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("Starting SSH portfolio", "host", host, "port", port)
	logger.Info("Connect with", "cmd", fmt.Sprintf("ssh localhost -p %s", port))

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != ssh.ErrServerClosed {
			logger.Error("Server error", "error", err)
			done <- syscall.SIGTERM
		}
	}()

	<-done
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger.Info("Shutting down…")
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Shutdown error", "error", err)
	}
}

func teaHandler(s ssh.Session) (tea.Model, []tea.ProgramOption) {
	pty, _, _ := s.Pty()
	w, h := 80, 24
	if pty.Window.Width > 0 {
		w = pty.Window.Width
		h = pty.Window.Height
	}
	return newModel(w, h, bm.MakeRenderer(s)), []tea.ProgramOption{tea.WithAltScreen()}
}
