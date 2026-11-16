package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/contract"
)

// browseCommand opens the interactive desk browser on a terminal.
func browseCommand(e *env) error {
	if err := needHome(e, "Não foi possível ler as capturas."); err != nil {
		return err
	}
	m := newBrowser(loadBrowserData(e), herdrRun)
	program := tea.NewProgram(m, tea.WithInput(e.in), tea.WithOutput(e.out))
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(e.errW, "A interface não pôde abrir:", err)
		return exitError(1)
	}
	return nil
}

// capture is one desk the browser can show: latest.json or a history entry.
type capture struct {
	title     string
	at        time.Time
	desk      contract.Snapshot
	err       error
	resumable int
	history   int // index for restore --from; 0 = latest
}

type browserData struct {
	version   string
	host      string
	captures  []capture
	pending   int
	conflicts int
	queueErr  bool
	unit      bool
	enrolled  bool
	loadErr   error
}

func loadBrowserData(e *env) browserData {
	data := browserData{version: version, unit: unitInstalled(e.home, e.goos)}
	data.host, _ = agent.CurrentHost()
	if config, err := agent.Load(agent.PathsFor(e.home).Config); err == nil {
		data.enrolled = tokenExists(config.TokenFile)
	}
	if queue, err := agent.ReadQueue(stateDir(e.home)); err == nil {
		data.pending, data.conflicts = len(queue.Pending), len(queue.Conflicts)
	} else {
		data.queueErr = true
	}
	latestID := ""
	if raw, err := os.ReadFile(latestPath(e.home)); err == nil {
		desk, err := contract.ValidateSnapshot(raw)
		c := capture{title: "atual", desk: desk, err: err}
		if at, ok := contract.ParseTime(desk.CapturedAt); ok {
			c.at = at
		}
		c.resumable = countResumable(desk)
		latestID = desk.SnapshotID
		data.captures = append(data.captures, c)
	} else if !errors.Is(err, os.ErrNotExist) {
		data.loadErr = err
	}
	entries, err := agent.ReadHistory(stateDir(e.home))
	if err != nil {
		data.loadErr = err
	}
	for i, entry := range entries {
		if entry.SnapshotID == latestID {
			continue
		}
		desk, err := entry.Snapshot()
		data.captures = append(data.captures, capture{
			title: fmt.Sprintf("#%d", i+1), at: entry.CapturedAt, desk: desk, err: err,
			resumable: entry.Resumable, history: i + 1,
		})
	}
	return data
}

func countResumable(desk contract.Snapshot) int {
	n := 0
	for _, pane := range desk.Panes {
		if len(pane.ResumeArgv) > 0 {
			n++
		}
	}
	return n
}

type browserMode int

const (
	modeBrowse browserMode = iota
	modeFilter
	modeSession
	modePlan
	modeRestoring
	modeHelp
)

type focusArea int

const (
	focusCaptures focusArea = iota
	focusPanes
)

type restoreDoneMsg struct {
	report agent.RestoreReport
	err    error
	plan   bool
}

type flashClearMsg struct{ id int }

type browser struct {
	data    browserData
	run     agent.Runner
	width   int
	height  int
	mode    browserMode
	focus   focusArea
	capIdx  int
	capTop  int
	paneIdx int
	paneTop int
	filter  textinput.Model
	session textinput.Model
	plan    []string
	planTop int
	flash   string
	flashOK bool
	flashID int
	now     func() time.Time
}

func newBrowser(data browserData, run agent.Runner) browser {
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "workspace, aba, pane, harness ou diretório"
	filter.CharLimit = 120
	session := textinput.New()
	session.Prompt = "sessão: "
	session.Placeholder = "volta"
	session.CharLimit = 64
	return browser{data: data, run: run, width: 100, height: 30, filter: filter, session: session, now: nowFunc}
}

func (m browser) Init() tea.Cmd { return nil }

func (m browser) current() (capture, bool) {
	if m.capIdx < 0 || m.capIdx >= len(m.data.captures) {
		return capture{}, false
	}
	return m.data.captures[m.capIdx], true
}

// visiblePanes applies the filter to the selected capture.
func (m browser) visiblePanes() []contract.Pane {
	c, ok := m.current()
	if !ok {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if query == "" {
		return c.desk.Panes
	}
	var out []contract.Pane
	for _, pane := range c.desk.Panes {
		hay := strings.ToLower(strings.Join([]string{pane.WorkspaceLabel, pane.TabLabel, pane.PaneLabel, pane.Harness, pane.Cwd, pane.SessionID}, " "))
		if strings.Contains(hay, query) {
			out = append(out, pane)
		}
	}
	return out
}

func (m browser) selectedPane() (contract.Pane, bool) {
	panes := m.visiblePanes()
	if m.paneIdx < 0 || m.paneIdx >= len(panes) {
		return contract.Pane{}, false
	}
	return panes[m.paneIdx], true
}

func (m *browser) setFlash(text string, ok bool) tea.Cmd {
	m.flash, m.flashOK = text, ok
	m.flashID++
	id := m.flashID
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return flashClearMsg{id: id} })
}

func (m browser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case flashClearMsg:
		if msg.id == m.flashID {
			m.flash = ""
		}
		return m, nil
	case restoreDoneMsg:
		return m.restoreDone(msg)
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m browser) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeHelp:
		m.mode = modeBrowse
		return m, nil
	case modeRestoring:
		return m, nil
	case modeFilter:
		switch key {
		case "esc":
			m.filter.SetValue("")
			m.filter.Blur()
			m.mode = modeBrowse
			m.paneIdx, m.paneTop = 0, 0
			return m, nil
		case "enter", "down", "up":
			m.filter.Blur()
			m.mode = modeBrowse
			m.focus = focusPanes
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.paneIdx, m.paneTop = 0, 0
		return m, cmd
	case modeSession:
		switch key {
		case "esc":
			m.session.Blur()
			m.mode = modeBrowse
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.session.Value())
			if err := agent.RejectSession(name); err != nil {
				return m, m.setFlash(restoreMessage(err), false)
			}
			m.session.Blur()
			m.mode = modeRestoring
			return m, m.restore(name, true)
		}
		var cmd tea.Cmd
		m.session, cmd = m.session.Update(msg)
		return m, cmd
	case modePlan:
		switch key {
		case "esc", "n", "q":
			m.mode = modeBrowse
			return m, m.setFlash("Nada foi executado.", true)
		case "y", "s", "enter":
			m.mode = modeRestoring
			return m, m.restore(strings.TrimSpace(m.session.Value()), false)
		case "down", "j":
			if m.planTop < len(m.plan)-1 {
				m.planTop++
			}
		case "up", "k":
			if m.planTop > 0 {
				m.planTop--
			}
		}
		return m, nil
	}

	switch key {
	case "q", "esc":
		if key == "esc" && m.filter.Value() != "" {
			m.filter.SetValue("")
			m.paneIdx, m.paneTop = 0, 0
			return m, nil
		}
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
	case "tab", "shift+tab":
		if m.focus == focusCaptures {
			m.focus = focusPanes
		} else {
			m.focus = focusCaptures
		}
	case "left", "h":
		m.focus = focusCaptures
	case "right", "l", "enter":
		m.focus = focusPanes
	case "/":
		m.mode = modeFilter
		m.focus = focusPanes
		return m, m.filter.Focus()
	case "down", "j":
		m.move(1)
	case "up", "k":
		m.move(-1)
	case "pgdown", "ctrl+d":
		m.move(m.listHeight() / 2)
	case "pgup", "ctrl+u":
		m.move(-m.listHeight() / 2)
	case "g", "home":
		m.move(-1 << 20)
	case "G", "end":
		m.move(1 << 20)
	case "c", "y":
		pane, ok := m.selectedPane()
		if !ok || len(pane.ResumeArgv) == 0 {
			return m, m.setFlash("Sem sessão para restaurar neste pane.", false)
		}
		return m, tea.Batch(tea.SetClipboard(commandLine(pane.ResumeArgv)), m.setFlash("Comando copiado. Nada foi executado.", true))
	case "r":
		c, ok := m.current()
		if !ok || c.err != nil {
			return m, m.setFlash("Não há captura válida para montar.", false)
		}
		m.mode = modeSession
		return m, m.session.Focus()
	}
	return m, nil
}

func (m *browser) move(delta int) {
	if m.focus == focusCaptures {
		m.capIdx = clamp(m.capIdx+delta, 0, len(m.data.captures)-1)
		m.capTop = scrollTo(m.capIdx, m.capTop, m.listHeight())
		m.paneIdx, m.paneTop = 0, 0
		return
	}
	m.paneIdx = clamp(m.paneIdx+delta, 0, len(m.visiblePanes())-1)
	m.paneTop = scrollTo(m.paneIdx, m.paneTop, m.paneRows())
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func scrollTo(idx, top, height int) int {
	if height < 1 {
		height = 1
	}
	if idx < top {
		return idx
	}
	if idx >= top+height {
		return idx - height + 1
	}
	return top
}

func (m browser) restore(session string, plan bool) tea.Cmd {
	c, _ := m.current()
	desk, run := c.desk, m.run
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		report, err := agent.Restore(ctx, session, desk, plan, run)
		return restoreDoneMsg{report: report, err: err, plan: plan}
	}
}

func (m browser) restoreDone(msg restoreDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.mode = modeBrowse
		return m, m.setFlash(restoreMessage(msg.err), false)
	}
	if msg.plan {
		m.plan = msg.report.Lines
		m.planTop = 0
		m.mode = modePlan
		return m, nil
	}
	m.mode = modeBrowse
	return m, m.setFlash(fmt.Sprintf("Mesa montada na sessão %s: %s em %s.", strings.TrimSpace(m.session.Value()),
		plural(msg.report.Panes, "pane", "panes"), plural(msg.report.Workspaces, "workspace", "workspaces")), true)
}

// Layout: header (2) + body + detail (4) + footer (1).
const detailHeight = 4

func (m browser) listHeight() int {
	h := m.height - 2 - detailHeight - 2
	if h < 3 {
		return 3
	}
	return h
}

func (m browser) paneRows() int { return m.listHeight() - 2 }

var (
	tuiBorder   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tuiSelected = lipgloss.NewStyle().Reverse(true)
	tuiFocusBar = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	tuiKey      = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

func (m browser) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "herdr-recall"
	return v
}

func (m browser) render() string {
	if m.mode == modeHelp {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(tuiBorder.Render(strings.Repeat("─", max(m.width, 1))))
	b.WriteString("\n")
	if len(m.data.captures) == 0 {
		b.WriteString("\n  Nenhuma captura nesta máquina ainda.\n")
		b.WriteString(styleMuted.Render("  Rode herdr-recall capture, ou instale o serviço com herdr-recall service install.") + "\n")
		b.WriteString("\n" + m.footer())
		return b.String()
	}
	left := m.capturesView()
	right := m.panesView(m.width - lipgloss.Width(left) - 3)
	sep := tuiBorder.Render(strings.Repeat("│\n", m.listHeight()-1) + "│")
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, " ", sep, " ", right))
	b.WriteString("\n")
	b.WriteString(tuiBorder.Render(strings.Repeat("─", max(m.width, 1))))
	b.WriteString("\n")
	b.WriteString(m.bottomView())
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m browser) header() string {
	left := styleBold.Render("herdr-recall") + styleMuted.Render(" "+m.data.version)
	if m.data.host != "" {
		left += styleMuted.Render(" · " + m.data.host)
	}
	var parts []string
	if !m.data.enrolled {
		parts = append(parts, styleWarn.Render("não enrolada"))
	}
	if m.data.unit {
		parts = append(parts, styleOK.Render("serviço instalado"))
	} else {
		parts = append(parts, styleWarn.Render("sem serviço"))
	}
	switch {
	case m.data.queueErr:
		parts = append(parts, styleFail.Render("fila ilegível"))
	case m.data.conflicts > 0:
		parts = append(parts, styleFail.Render(fmt.Sprintf("%d em quarentena", m.data.conflicts)))
	case m.data.pending > 0:
		parts = append(parts, styleWarn.Render(fmt.Sprintf("fila %d", m.data.pending)))
	default:
		parts = append(parts, styleOK.Render("fila vazia"))
	}
	right := strings.Join(parts, styleMuted.Render(" · "))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left+" "+right, m.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

const capturesWidth = 26

func (m browser) capturesView() string {
	height := m.listHeight()
	title := "Capturas"
	if m.focus == focusCaptures {
		title = tuiFocusBar.Render("▌Capturas")
	} else {
		title = styleMuted.Render(" " + title)
	}
	lines := []string{title}
	for i := m.capTop; i < len(m.data.captures) && len(lines) < height; i++ {
		c := m.data.captures[i]
		when := "—"
		if !c.at.IsZero() {
			when = localStamp(c.at)
		}
		text := fmt.Sprintf(" %-5s %s %4d", c.title, when, len(c.desk.Panes))
		if c.err != nil {
			text = fmt.Sprintf(" %-5s %s inválida", c.title, when)
		}
		text = pad(ansi.Truncate(text, capturesWidth, "…"), capturesWidth)
		if i == m.capIdx {
			if m.focus == focusCaptures {
				text = tuiSelected.Render(text)
			} else {
				text = styleBold.Render(text)
			}
		}
		lines = append(lines, text)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", capturesWidth))
	}
	return strings.Join(lines, "\n")
}

func (m browser) panesView(width int) string {
	if width < 20 {
		width = 20
	}
	height := m.listHeight()
	c, _ := m.current()
	panes := m.visiblePanes()
	summary := "Mesa"
	if !c.at.IsZero() {
		summary = fmt.Sprintf("Mesa de %s (%s)", localStamp(c.at), ago(m.now(), c.at))
	}
	summary += styleMuted.Render(fmt.Sprintf(" · %s · %s", plural(len(c.desk.Panes), "pane", "panes"), plural(c.resumable, "retomável", "retomáveis")))
	if m.focus == focusPanes {
		summary = tuiFocusBar.Render("▌") + summary
	} else {
		summary = " " + summary
	}
	cols := []int{14, 12, 12, 9}
	lines := []string{ansi.Truncate(summary, width, "…")}
	if m.mode == modeFilter || m.filter.Value() != "" {
		m.filter.SetWidth(width - 4)
		lines = append(lines, " "+m.filter.View()+styleMuted.Render(fmt.Sprintf("  %d/%d", len(panes), len(c.desk.Panes))))
	} else {
		lines = append(lines, styleMuted.Render(" "+pad("workspace", cols[0])+" "+pad("aba", cols[1])+" "+pad("pane", cols[2])+" "+pad("harness", cols[3])+" diretório"))
	}
	if c.err != nil {
		lines = append(lines, styleFail.Render(" Esta captura não é válida e não pode ser mostrada."))
	} else if len(panes) == 0 {
		if m.filter.Value() != "" {
			lines = append(lines, styleMuted.Render(" Nenhum pane com esse filtro. Esc limpa."))
		} else {
			lines = append(lines, styleMuted.Render(" Nenhum pane nesta captura."))
		}
	}
	for i := m.paneTop; i < len(panes) && len(lines) < height; i++ {
		pane := panes[i]
		mark := " "
		if len(pane.ResumeArgv) > 0 {
			mark = styleOK.Render("●")
			if pane.Confidence == contract.ConfidenceLow {
				mark = styleWarn.Render("●")
			}
		}
		fixed := 1 + cols[0] + 1 + cols[1] + 1 + cols[2] + 1 + cols[3] + 1
		dirWidth := width - fixed
		if dirWidth < 8 {
			dirWidth = 8
		}
		row := pad(ansi.Truncate(cell(pane.WorkspaceLabel), cols[0], "…"), cols[0]) + " " +
			pad(ansi.Truncate(cell(pane.TabLabel), cols[1], "…"), cols[1]) + " " +
			pad(ansi.Truncate(cell(pane.PaneLabel), cols[2], "…"), cols[2]) + " " +
			pad(ansi.Truncate(cell(pane.Harness), cols[3], "…"), cols[3]) + " " +
			middleTruncate(shortHome(cell(pane.Cwd)), dirWidth)
		if i == m.paneIdx && m.focus == focusPanes {
			row = tuiSelected.Render(pad(row, width-1))
		} else if i == m.paneIdx {
			row = styleBold.Render(row)
		}
		lines = append(lines, mark+row)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m browser) bottomView() string {
	width := max(m.width, 20)
	var lines []string
	switch m.mode {
	case modeSession:
		m.session.SetWidth(30)
		lines = append(lines,
			styleBold.Render("Montar esta captura numa sessão nomeada do Herdr"),
			styleMuted.Render("A sessão precisa estar aberta (herdr --session NOME) e vazia. default não serve."),
			m.session.View(),
			styleMuted.Render("enter mostra o plano · esc cancela"))
	case modeRestoring:
		lines = append(lines, styleAccent.Render("Conversando com o Herdr…"))
	case modePlan:
		lines = append(lines, styleBold.Render("Plano para a sessão "+strings.TrimSpace(m.session.Value()))+styleMuted.Render(" · y monta · esc cancela · ↑↓ rola"))
		for i := m.planTop; i < len(m.plan) && len(lines) < detailHeight; i++ {
			lines = append(lines, styleMuted.Render(ansi.Truncate(m.plan[i], width, "…")))
		}
	default:
		pane, ok := m.selectedPane()
		if !ok {
			break
		}
		lines = append(lines, styleMuted.Render("diretório ")+ansi.Truncate(cell(pane.Cwd), width-10, "…"))
		session := cell(pane.SessionID)
		if pane.Confidence != "" {
			session += styleMuted.Render(" · confiança " + confidenceCell(pane.Confidence))
		}
		if len(pane.AmbiguousWith) > 0 {
			session += styleWarn.Render(" · ambíguo com " + strings.Join(pane.AmbiguousWith, ", "))
		}
		lines = append(lines, styleMuted.Render("sessão    ")+session)
		if len(pane.ResumeArgv) > 0 {
			lines = append(lines, styleMuted.Render("comando   ")+styleAccent.Render(ansi.Truncate(commandLine(pane.ResumeArgv), width-10, "…")))
		} else {
			lines = append(lines, styleMuted.Render("comando   Sem sessão para restaurar."))
		}
	}
	for len(lines) < detailHeight {
		lines = append(lines, "")
	}
	return strings.Join(lines[:detailHeight], "\n")
}

func (m browser) footer() string {
	if m.flash != "" {
		if m.flashOK {
			return styleOK.Render(m.flash)
		}
		return styleWarn.Render(m.flash)
	}
	keys := [][2]string{{"tab", "painel"}, {"↑↓", "move"}, {"/", "filtra"}, {"c", "copia comando"}, {"r", "monta numa sessão"}, {"?", "ajuda"}, {"q", "sai"}}
	var parts []string
	for _, k := range keys {
		parts = append(parts, tuiKey.Render(k[0])+" "+styleMuted.Render(k[1]))
	}
	return ansi.Truncate(strings.Join(parts, styleMuted.Render(" · ")), max(m.width, 20), "…")
}

func (m browser) helpView() string {
	rows := [][2]string{
		{"tab, ←/→, h/l", "alterna entre capturas e panes"},
		{"↑/↓, j/k", "move a seleção"},
		{"pgup/pgdn, ctrl+u/d", "meia página"},
		{"g / G", "primeiro / último"},
		{"/", "filtra panes por workspace, aba, pane, harness, diretório ou sessão"},
		{"esc", "limpa o filtro; sem filtro, sai"},
		{"c ou y", "copia o comando do pane (OSC52). Nada é executado"},
		{"r", "monta a captura selecionada numa sessão nomeada vazia: mostra o plano antes"},
		{"q, ctrl+c", "sai"},
	}
	var b strings.Builder
	b.WriteString(styleBold.Render("herdr-recall · atalhos") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + tuiKey.Render(pad(r[0], 22)) + r[1] + "\n")
	}
	b.WriteString("\n" + styleMuted.Render("● verde: comando com confiança alta · ● amarelo: confiança baixa") + "\n")
	b.WriteString(styleMuted.Render("A cópia usa OSC52; nem todo terminal aceita. O comando completo também sai em herdr-recall latest.") + "\n\n")
	b.WriteString(styleMuted.Render("qualquer tecla volta"))
	return b.String()
}

func shortHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// middleTruncate keeps the start and the end of a path, which carry the most
// meaning.
func middleTruncate(s string, width int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	if width < 5 {
		return ansi.Truncate(s, width, "")
	}
	runes := []rune(s)
	keepEnd := (width - 1) / 2
	keepStart := width - 1 - keepEnd
	return string(runes[:keepStart]) + "…" + string(runes[len(runes)-keepEnd:])
}
