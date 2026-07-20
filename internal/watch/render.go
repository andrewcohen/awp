package watch

import (
	"fmt"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/charmbracelet/lipgloss"
)

var (
	styTitle   = lipgloss.NewStyle().Bold(true)
	styMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styCurrent = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	styPass    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success))
	styFail    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Bold(true)
	styDone    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success))
	styWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Bold(true)
)

// Render produces the combined todos+loop panel for the given state: the
// Header line followed by the RenderBody section.
func Render(loop Loop, workspace string, st State) string {
	return Header(workspace, st) + "\n\n" + RenderBody(loop, st)
}

// Header renders the one-line panel header: "awp watch · <workspace>" plus the
// agent status and time-on-unit. It's split out so the deck's watch overlay
// can pin it as a sticky title without the body's task list repeating it.
func Header(workspace string, st State) string {
	head := styTitle.Render("awp watch") + styMuted.Render(" · "+workspace)
	status := st.AgentStatus
	if status == "" {
		status = "unknown"
	}
	head += "  " + styCurrent.Render(status)
	if !st.UnitStart.IsZero() {
		head += styMuted.Render("  ·  " + since(st.UnitStart, st.Now) + " on task")
	}
	return head
}

// RenderBody renders everything below the Header: the task list with the
// current unit's loop ring, gate lights, and churn line.
func RenderBody(loop Loop, st State) string {
	var b strings.Builder
	cur := st.CurrentUnit()

	if len(st.Todos) == 0 {
		// Degraded view: one implicit unit, just the loop.
		b.WriteString(styMuted.Render("TASKS  (no todo list — showing current work)") + "\n")
		b.WriteString(renderUnitBody(loop, st))
		return b.String()
	}

	b.WriteString(styMuted.Render(fmt.Sprintf("TASKS  %d/%d", st.DoneCount(), len(st.Todos))) + "\n")
	// Order: the current unit on top (with its loop body), then the upcoming
	// units, then the completed ones — so the eye lands on what's happening
	// now and what's next, with finished work sinking to the bottom. Each
	// group keeps the todo list's own order.
	if cur >= 0 && cur < len(st.Todos) {
		b.WriteString("  " + styCurrent.Render("▶ "+st.Todos[cur].Content) + styMuted.Render("   ← current") + "\n")
		b.WriteString(renderUnitBody(loop, st))
	}
	for i, t := range st.Todos {
		if i == cur || t.Status == "completed" {
			continue
		}
		b.WriteString("  " + styMuted.Render("○ "+t.Content) + "\n")
	}
	for i, t := range st.Todos {
		if i == cur || t.Status != "completed" {
			continue
		}
		b.WriteString("  " + styDone.Render("✔ ") + styMuted.Render(t.Content) + "\n")
	}
	return b.String()
}

// renderUnitBody renders the loop ring, gate lights, and churn line for the
// current unit, indented under its todo.
func renderUnitBody(loop Loop, st State) string {
	var b strings.Builder
	b.WriteString("      " + styMuted.Render("loop   ") + ring(loop, st.CurrentPhase) + "\n")
	if gl := gateLine(st.Gates); gl != "" {
		b.WriteString("      " + styMuted.Render("gates  ") + gl + "\n")
	}
	if churn := churnLine(st); churn != "" {
		b.WriteString("      " + churn + "\n")
	}
	return b.String()
}

// ring renders the phase sequence with the current phase highlighted.
func ring(loop Loop, current string) string {
	parts := make([]string, 0, len(loop.Phases))
	for _, p := range loop.Phases {
		if p == current {
			parts = append(parts, styCurrent.Render("▶"+strings.ToUpper(p)))
		} else {
			parts = append(parts, styMuted.Render(p))
		}
	}
	return strings.Join(parts, styMuted.Render(" ─ "))
}

// gateLine renders the gate lights: ✔ pass, ✗ fail ×N, ○ not yet run.
func gateLine(gates []GateState) string {
	if len(gates) == 0 {
		return ""
	}
	cells := make([]string, 0, len(gates))
	for _, g := range gates {
		switch g.Result {
		case "pass":
			cells = append(cells, styPass.Render("✔ "+g.Name))
		case "fail":
			cell := styFail.Render("✗ " + g.Name)
			if g.RedCount > 1 {
				cell += styFail.Render(fmt.Sprintf(" ×%d", g.RedCount))
			}
			cells = append(cells, cell)
		default:
			cells = append(cells, styMuted.Render("○ "+g.Name))
		}
	}
	return strings.Join(cells, "  ")
}

// churnLine surfaces the worst-thrashing gate plus time on the unit.
func churnLine(st State) string {
	var worst GateState
	for _, g := range st.Gates {
		if g.RedCount > worst.RedCount {
			worst = g
		}
	}
	var segs []string
	if worst.RedCount >= 2 {
		segs = append(segs, styWarn.Render(fmt.Sprintf("⇄ implement⇄%s %d×", worst.Name, worst.RedCount)))
	}
	if !st.UnitStart.IsZero() {
		segs = append(segs, styMuted.Render(since(st.UnitStart, st.Now)+" on task"))
	}
	if worst.RedCount >= 3 {
		segs = append(segs, styWarn.Render("⚠ thrash"))
	}
	return strings.Join(segs, styMuted.Render("  ·  "))
}

func since(t, now time.Time) string {
	d := now.Sub(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
