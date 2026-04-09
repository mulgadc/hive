/*
Copyright © 2026 Mulga Defense Corporation

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

// Package ui presents the interactive installer TUI using bubbletea and lipgloss.
package ui

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mulgadc/spinifex/cmd/installer/branding"
	"github.com/mulgadc/spinifex/cmd/installer/install"
)

// screen represents which step of the wizard is active.
type screen int

const (
	screenWelcome screen = iota
	screenDisk
	screenDiskConfirm
	screenNetwork
	screenIdentity
	screenPassword
	screenJoinConfig
	screenCACertPrompt
	screenCACert
	screenConfirm
	screenDone // signals completion; program exits
)

// networkField tracks which text input is focused on the network screen.
type networkField int

const (
	fieldIP networkField = iota
	fieldMask
	fieldGateway
	fieldNIC
	fieldNetworkCount
)

// model is the top-level bubbletea model for the installer wizard.
type model struct {
	screen screen
	width  int
	height int

	// Disk selection
	disks      []diskInfo
	diskCursor int
	eraseInput textinput.Model

	// Network
	netInputs      [3]textinput.Model // IP, mask, gateway
	netFocus       networkField
	nics           []string
	nicCursor      int
	nicManualInput textinput.Model // used when no NICs are auto-detected

	// Identity
	hostnameInput textinput.Model
	clusterRole   int // 0 = init, 1 = join

	// Join config
	joinIPInput   textinput.Model
	joinPortInput textinput.Model

	// Password
	passwordInput        textinput.Model
	passwordConfirmInput textinput.Model
	passwordFocus        int // 0 = password, 1 = confirm

	// CA cert
	hasCACert   int // 0 = no, 1 = yes
	caCertInput textinput.Model

	// Accumulated validation error shown on current screen
	validationErr string

	// Final result — set when screenDone is reached
	result *install.Config
	err    error
}

// Run launches the bubbletea program connected to ttyPath and returns the
// completed Config when the user finishes the wizard.
func Run(ttyPath string) (*install.Config, error) {
	disks, err := availableDisks()
	if err != nil {
		return nil, fmt.Errorf("listing disks: %w", err)
	}
	if len(disks) == 0 {
		return nil, errors.New("no block devices found")
	}

	nics, err := availableNICs()
	if err != nil {
		return nil, fmt.Errorf("listing network interfaces: %w", err)
	}

	m := newModel(disks, nics)

	var opts []tea.ProgramOption
	opts = append(opts, tea.WithAltScreen())

	if ttyPath != "" {
		tty, err := os.OpenFile(ttyPath, os.O_RDWR, 0)
		if err == nil {
			opts = append(opts, tea.WithInput(tty), tea.WithOutput(tty))
		}
	}

	p := tea.NewProgram(m, opts...)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}

	fm := final.(model)
	if fm.err != nil {
		return nil, fm.err
	}
	return fm.result, nil
}

func newModel(disks []diskInfo, nics []string) model {
	eraseIn := textinput.New()
	eraseIn.Placeholder = "yes"
	eraseIn.CharLimit = 3

	ipIn := textinput.New()
	ipIn.Placeholder = "192.168.1.10"

	maskIn := textinput.New()
	maskIn.Placeholder = "255.255.255.0 or 24"

	gwIn := textinput.New()
	gwIn.Placeholder = "192.168.1.1"
	ipIn.Focus()

	nicManualIn := textinput.New()
	nicManualIn.Placeholder = "e.g. eth0, enp0s1"
	nicManualIn.CharLimit = 32

	hostnameIn := textinput.New()
	hostnameIn.Placeholder = "node1"
	hostnameIn.CharLimit = 64

	joinIPIn := textinput.New()
	joinIPIn.Placeholder = "192.168.1.10"

	joinPortIn := textinput.New()
	joinPortIn.Placeholder = "4432"
	joinPortIn.CharLimit = 5

	passIn := textinput.New()
	passIn.Placeholder = "Root password"
	passIn.EchoMode = textinput.EchoPassword
	passIn.CharLimit = 128

	passConfirmIn := textinput.New()
	passConfirmIn.Placeholder = "Confirm password"
	passConfirmIn.EchoMode = textinput.EchoPassword
	passConfirmIn.CharLimit = 128

	caCertIn := textinput.New()
	caCertIn.Placeholder = "-----BEGIN CERTIFICATE-----"
	caCertIn.CharLimit = 0

	return model{
		screen:               screenWelcome,
		disks:                disks,
		nics:                 nics,
		eraseInput:           eraseIn,
		netInputs:            [3]textinput.Model{ipIn, maskIn, gwIn},
		nicManualInput:       nicManualIn,
		hostnameInput:        hostnameIn,
		passwordInput:        passIn,
		passwordConfirmInput: passConfirmIn,
		joinIPInput:          joinIPIn,
		joinPortInput:        joinPortIn,
		caCertInput:          caCertIn,
	}
}

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	styleLogo = lipgloss.NewStyle().
			Foreground(branding.ColorPrimary).
			Bold(true)

	styleTitle = lipgloss.NewStyle().
			Foreground(branding.ColorPrimary).
			Bold(true).
			MarginBottom(1)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(branding.ColorMuted)

	styleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(branding.ColorBorder).
			Padding(1, 2)

	styleSelected = lipgloss.NewStyle().
			Foreground(branding.ColorBackground).
			Background(branding.ColorPrimary).
			Bold(true)

	styleWarning = lipgloss.NewStyle().
			Foreground(branding.ColorWarning).
			Bold(true)

	styleError = lipgloss.NewStyle().
			Foreground(branding.ColorError)

	styleMuted = lipgloss.NewStyle().
			Foreground(branding.ColorMuted)

	styleSuccess = lipgloss.NewStyle().
			Foreground(branding.ColorSuccess)

	styleLabel = lipgloss.NewStyle().
			Foreground(branding.ColorAccent).
			Bold(true)

	styleHelp = lipgloss.NewStyle().
			Foreground(branding.ColorMuted).
			MarginTop(1)
)

// ── Init / Update / View ──────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		m.validationErr = ""
		switch msg.String() {
		case "ctrl+c":
			m.err = errors.New("installation cancelled")
			return m, tea.Quit
		}
		return m.handleKey(msg)
	}

	// Forward to active input
	return m.updateActiveInput(msg)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.screen {
	case screenWelcome:
		if key == "enter" || key == " " {
			m.screen = screenDisk
		}

	case screenDisk:
		switch key {
		case "up", "k":
			if m.diskCursor > 0 {
				m.diskCursor--
			}
		case "down", "j":
			if m.diskCursor < len(m.disks)-1 {
				m.diskCursor++
			}
		case "enter":
			m.screen = screenDiskConfirm
			m.eraseInput.Focus()
			m.eraseInput.SetValue("")
		}

	case screenDiskConfirm:
		switch key {
		case "enter":
			if strings.ToLower(strings.TrimSpace(m.eraseInput.Value())) != "yes" {
				m.validationErr = "Type 'yes' to confirm disk erasure"
				return m, nil
			}
			m.screen = screenNetwork
			m.netFocus = fieldIP
			m.netInputs[0].Focus()
			m.netInputs[1].Blur()
			m.netInputs[2].Blur()
		case "esc":
			m.screen = screenDisk
			return m, nil
		default:
			var cmd tea.Cmd
			m.eraseInput, cmd = m.eraseInput.Update(msg)
			return m, cmd
		}

	case screenNetwork:
		switch key {
		case "tab", "down":
			m.netFocus = (m.netFocus + 1) % fieldNetworkCount
			m = m.withFocusedNetworkField()
		case "shift+tab", "up":
			m.netFocus = (m.netFocus - 1 + fieldNetworkCount) % fieldNetworkCount
			m = m.withFocusedNetworkField()
		case "enter":
			if m.netFocus < fieldNIC {
				m.netFocus++
				m = m.withFocusedNetworkField()
				return m, nil
			}
			// Validate all fields
			ip := strings.TrimSpace(m.netInputs[fieldIP].Value())
			mask := strings.TrimSpace(m.netInputs[fieldMask].Value())
			gw := strings.TrimSpace(m.netInputs[fieldGateway].Value())
			if net.ParseIP(ip) == nil {
				m.validationErr = "Invalid management IP address"
				return m, nil
			}
			if !validSubnetMask(mask) {
				m.validationErr = "Invalid subnet mask (e.g. 255.255.255.0 or /24)"
				return m, nil
			}
			if net.ParseIP(gw) == nil {
				m.validationErr = "Invalid gateway address"
				return m, nil
			}
			if len(m.nics) == 0 {
				if strings.TrimSpace(m.nicManualInput.Value()) == "" {
					m.validationErr = "Enter interface name (e.g. eth0, enp0s1)"
					return m, nil
				}
			}
			m.screen = screenIdentity
			m.hostnameInput.Focus()
		case "left", "h":
			if m.netFocus == fieldNIC && len(m.nics) > 0 && m.nicCursor > 0 {
				m.nicCursor--
			}
		case "right", "l":
			if m.netFocus == fieldNIC && len(m.nics) > 0 && m.nicCursor < len(m.nics)-1 {
				m.nicCursor++
			}
		default:
			if m.netFocus < fieldNIC {
				var cmd tea.Cmd
				m.netInputs[m.netFocus], cmd = m.netInputs[m.netFocus].Update(msg)
				return m, cmd
			}
			if m.netFocus == fieldNIC && len(m.nics) == 0 {
				var cmd tea.Cmd
				m.nicManualInput, cmd = m.nicManualInput.Update(msg)
				return m, cmd
			}
		}

	case screenIdentity:
		switch key {
		case "tab", "down":
			if m.hostnameInput.Focused() {
				m.hostnameInput.Blur()
			} else {
				m.hostnameInput.Focus()
			}
		case "left", "right":
			if m.hostnameInput.Focused() {
				// cursor movement inside the hostname field
				var cmd tea.Cmd
				m.hostnameInput, cmd = m.hostnameInput.Update(msg)
				return m, cmd
			}
			if key == "left" {
				m.clusterRole = 0
			} else {
				m.clusterRole = 1
			}
		case "enter":
			if m.hostnameInput.Focused() {
				if strings.TrimSpace(m.hostnameInput.Value()) == "" {
					m.validationErr = "Hostname is required"
					return m, nil
				}
				m.hostnameInput.Blur()
				return m, nil
			}
			if strings.TrimSpace(m.hostnameInput.Value()) == "" {
				m.validationErr = "Hostname is required"
				m.hostnameInput.Focus()
				return m, nil
			}
			m.screen = screenPassword
			m.passwordInput.Focus()
			m.passwordFocus = 0
		default:
			if m.hostnameInput.Focused() {
				var cmd tea.Cmd
				m.hostnameInput, cmd = m.hostnameInput.Update(msg)
				return m, cmd
			}
		}

	case screenPassword:
		switch key {
		case "tab", "down":
			m.passwordInput.Blur()
			m.passwordConfirmInput.Blur()
			if m.passwordFocus == 0 {
				m.passwordConfirmInput.Focus()
				m.passwordFocus = 1
			} else {
				m.passwordInput.Focus()
				m.passwordFocus = 0
			}
		case "shift+tab", "up":
			m.passwordInput.Blur()
			m.passwordConfirmInput.Blur()
			if m.passwordFocus == 1 {
				m.passwordInput.Focus()
				m.passwordFocus = 0
			} else {
				m.passwordConfirmInput.Focus()
				m.passwordFocus = 1
			}
		case "enter":
			if m.passwordFocus == 0 {
				// Move to confirm field on first enter
				m.passwordInput.Blur()
				m.passwordConfirmInput.Focus()
				m.passwordFocus = 1
				return m, nil
			}
			pw := m.passwordInput.Value()
			confirm := m.passwordConfirmInput.Value()
			if pw == "" {
				m.validationErr = "Password is required"
				return m, nil
			}
			if pw != confirm {
				m.validationErr = "Passwords do not match"
				return m, nil
			}
			m.validationErr = ""
			if m.clusterRole == 1 {
				m.screen = screenJoinConfig
				m.joinIPInput.Focus()
			} else {
				m.screen = screenCACertPrompt
			}
		case "esc":
			m.passwordInput.Blur()
			m.passwordConfirmInput.Blur()
			m.screen = screenIdentity
			m.hostnameInput.Focus()
		default:
			var cmd tea.Cmd
			if m.passwordFocus == 0 {
				m.passwordInput, cmd = m.passwordInput.Update(msg)
			} else {
				m.passwordConfirmInput, cmd = m.passwordConfirmInput.Update(msg)
			}
			return m, cmd
		}

	case screenJoinConfig:
		switch key {
		case "tab", "down":
			if m.joinIPInput.Focused() {
				m.joinIPInput.Blur()
				m.joinPortInput.Focus()
			} else {
				m.joinPortInput.Blur()
				m.joinIPInput.Focus()
			}
		case "enter":
			if m.joinIPInput.Focused() {
				m.joinIPInput.Blur()
				m.joinPortInput.Focus()
				return m, nil
			}
			joinIP := strings.TrimSpace(m.joinIPInput.Value())
			if net.ParseIP(joinIP) == nil {
				m.validationErr = "Invalid primary node IP"
				return m, nil
			}
			m.screen = screenCACertPrompt
		case "esc":
			m.screen = screenIdentity
		default:
			var cmd tea.Cmd
			if m.joinIPInput.Focused() {
				m.joinIPInput, cmd = m.joinIPInput.Update(msg)
			} else {
				m.joinPortInput, cmd = m.joinPortInput.Update(msg)
			}
			return m, cmd
		}

	case screenCACertPrompt:
		switch key {
		case "left", "h", "right", "l":
			if m.hasCACert == 0 {
				m.hasCACert = 1
			} else {
				m.hasCACert = 0
			}
		case "enter":
			if m.hasCACert == 1 {
				m.screen = screenCACert
				m.caCertInput.Focus()
			} else {
				m.screen = screenConfirm
			}
		}

	case screenCACert:
		switch key {
		case "enter":
			cert := strings.TrimSpace(m.caCertInput.Value())
			if !strings.Contains(cert, "BEGIN CERTIFICATE") {
				m.validationErr = "Does not look like a PEM certificate"
				return m, nil
			}
			m.screen = screenConfirm
		case "esc":
			m.screen = screenCACertPrompt
		default:
			var cmd tea.Cmd
			m.caCertInput, cmd = m.caCertInput.Update(msg)
			return m, cmd
		}

	case screenConfirm:
		switch key {
		case "enter", "y", "Y":
			m.result = m.buildConfig()
			m.screen = screenDone
			return m, tea.Quit
		case "n", "N", "esc":
			m.err = errors.New("installation cancelled")
			return m, tea.Quit
		}
	}

	return m, nil
}

func (m model) withFocusedNetworkField() model {
	for i := range m.netInputs {
		if networkField(i) == m.netFocus {
			m.netInputs[i].Focus()
		} else {
			m.netInputs[i].Blur()
		}
	}
	// When no NICs were auto-detected, the NIC field is a text input.
	if m.netFocus == fieldNIC && len(m.nics) == 0 {
		m.nicManualInput.Focus()
	} else {
		m.nicManualInput.Blur()
	}
	return m
}

func (m model) updateActiveInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Delegate to the currently-focused text input for cursor blink, etc.
	switch m.screen {
	case screenDiskConfirm:
		var cmd tea.Cmd
		m.eraseInput, cmd = m.eraseInput.Update(msg)
		return m, cmd
	case screenNetwork:
		if m.netFocus < fieldNIC {
			var cmd tea.Cmd
			m.netInputs[m.netFocus], cmd = m.netInputs[m.netFocus].Update(msg)
			return m, cmd
		}
		if m.netFocus == fieldNIC && len(m.nics) == 0 {
			var cmd tea.Cmd
			m.nicManualInput, cmd = m.nicManualInput.Update(msg)
			return m, cmd
		}
	case screenIdentity:
		if m.hostnameInput.Focused() {
			var cmd tea.Cmd
			m.hostnameInput, cmd = m.hostnameInput.Update(msg)
			return m, cmd
		}
	case screenPassword:
		var cmd tea.Cmd
		if m.passwordFocus == 0 {
			m.passwordInput, cmd = m.passwordInput.Update(msg)
		} else {
			m.passwordConfirmInput, cmd = m.passwordConfirmInput.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	w := m.width
	if w == 0 {
		w = 80
	}

	var content string
	switch m.screen {
	case screenWelcome:
		content = m.viewWelcome(w)
	case screenDisk:
		content = m.viewDisk(w)
	case screenDiskConfirm:
		content = m.viewDiskConfirm(w)
	case screenNetwork:
		content = m.viewNetwork(w)
	case screenIdentity:
		content = m.viewIdentity(w)
	case screenPassword:
		content = m.viewPassword(w)
	case screenJoinConfig:
		content = m.viewJoinConfig(w)
	case screenCACertPrompt:
		content = m.viewCACertPrompt(w)
	case screenCACert:
		content = m.viewCACert(w)
	case screenConfirm:
		content = m.viewConfirm(w)
	case screenDone:
		content = m.viewDone(w)
	}

	return content
}

func (m model) viewWelcome(w int) string {
	logo := styleLogo.Render(branding.Logo)
	subtitle := styleSubtitle.Render(branding.Subtitle)
	publisher := styleMuted.Render(branding.Publisher)

	warning := styleWarning.Render("WARNING: Installation will erase the selected disk entirely.")
	help := styleHelp.Render("Press Enter to begin")

	body := lipgloss.JoinVertical(lipgloss.Center,
		logo,
		subtitle,
		publisher,
		"",
		warning,
		"",
		help,
	)

	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 72)).Render(body),
	)
}

func (m model) viewDisk(w int) string {
	title := styleTitle.Render("Select Installation Disk")
	subtitle := styleMuted.Render("All data on the selected disk will be permanently erased.")

	var rows []string
	for i, d := range m.disks {
		line := fmt.Sprintf("  %-20s  %-8s  %s", d.Path, d.Size, d.Model)
		if i == m.diskCursor {
			line = styleSelected.Render("> " + line[2:])
		} else {
			line = styleMuted.Render(line)
		}
		rows = append(rows, line)
	}

	help := styleHelp.Render("↑/↓ to select • Enter to confirm")
	body := lipgloss.JoinVertical(lipgloss.Left, append([]string{title, subtitle, ""}, append(rows, "", help)...)...)

	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 72)).Render(body),
	)
}

func (m model) viewDiskConfirm(w int) string {
	title := styleTitle.Render("Confirm Disk Erasure")
	disk := styleLabel.Render(m.disks[m.diskCursor].Path)
	msg := fmt.Sprintf("All data on %s will be permanently erased.\nType 'yes' to confirm:", disk)

	var lines []string
	lines = append(lines, title, msg, "", m.eraseInput.View())
	if m.validationErr != "" {
		lines = append(lines, "", styleError.Render(m.validationErr))
	}
	lines = append(lines, styleHelp.Render("Enter to confirm • Esc to go back"))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 64)).Render(body),
	)
}

func (m model) viewNetwork(w int) string {
	title := styleTitle.Render("Network Configuration")

	labels := []string{"Management IP", "Subnet mask", "Default gateway"}
	var rows []string
	for i, inp := range m.netInputs {
		label := styleLabel.Render(labels[i])
		if networkField(i) == m.netFocus {
			rows = append(rows, label, inp.View(), "")
		} else {
			rows = append(rows, label, styleMuted.Render(inp.Value()), "")
		}
	}

	// NIC selector (or manual input if no NICs were auto-detected)
	nicLabel := styleLabel.Render("OVN network interface")
	var nicLine string
	if len(m.nics) == 0 {
		nicLine = m.nicManualInput.View()
	} else {
		var parts []string
		for i, nic := range m.nics {
			if i == m.nicCursor {
				if m.netFocus == fieldNIC {
					parts = append(parts, styleSelected.Render(" "+nic+" "))
				} else {
					parts = append(parts, styleLabel.Render("["+nic+"]"))
				}
			} else {
				parts = append(parts, styleMuted.Render(nic))
			}
		}
		nicLine = strings.Join(parts, "  ")
	}
	rows = append(rows, nicLabel, nicLine)

	var errLine []string
	if m.validationErr != "" {
		errLine = []string{"", styleError.Render(m.validationErr)}
	}

	var helpText string
	if len(m.nics) == 0 {
		helpText = "Tab/↑↓ to move • Enter to proceed"
	} else {
		helpText = "Tab/↑↓ to move • ←/→ to select NIC • Enter to proceed"
	}
	help := styleHelp.Render(helpText)
	all := append([]string{title, ""}, append(rows, append(errLine, "", help)...)...)
	body := lipgloss.JoinVertical(lipgloss.Left, all...)

	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 64)).Render(body),
	)
}

func (m model) viewIdentity(w int) string {
	title := styleTitle.Render("Node Identity")

	hostnameLabel := styleLabel.Render("Hostname")

	roleLabel := styleLabel.Render("Cluster role")
	roles := []string{"Initialize new cluster", "Join existing cluster"}
	var roleParts []string
	for i, r := range roles {
		if i == m.clusterRole && !m.hostnameInput.Focused() {
			roleParts = append(roleParts, styleSelected.Render(" "+r+" "))
		} else if i == m.clusterRole {
			roleParts = append(roleParts, styleLabel.Render("["+r+"]"))
		} else {
			roleParts = append(roleParts, styleMuted.Render(r))
		}
	}

	var lines []string
	lines = append(lines, title, "", hostnameLabel, m.hostnameInput.View(), "", roleLabel, strings.Join(roleParts, "  "))
	if m.validationErr != "" {
		lines = append(lines, "", styleError.Render(m.validationErr))
	}
	lines = append(lines, styleHelp.Render("Tab to toggle focus • ←/→ to select role • Enter to proceed"))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 64)).Render(body),
	)
}

func (m model) viewPassword(w int) string {
	title := styleTitle.Render("Root Password")
	passLabel := styleLabel.Render("Password")
	confirmLabel := styleLabel.Render("Confirm password")

	var lines []string
	lines = append(lines, title, "", passLabel, m.passwordInput.View(), "", confirmLabel, m.passwordConfirmInput.View())
	if m.validationErr != "" {
		lines = append(lines, "", styleError.Render(m.validationErr))
	}
	lines = append(lines, "", styleHelp.Render("Tab to move • Enter to proceed • Esc to go back"))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 64)).Render(body),
	)
}

func (m model) viewJoinConfig(w int) string {
	title := styleTitle.Render("Join Existing Cluster")
	ipLabel := styleLabel.Render("Primary node IP")
	portLabel := styleLabel.Render("Formation port")

	var lines []string
	lines = append(lines, title, "", ipLabel, m.joinIPInput.View(), "", portLabel, m.joinPortInput.View())
	if m.validationErr != "" {
		lines = append(lines, "", styleError.Render(m.validationErr))
	}
	lines = append(lines, styleHelp.Render("Tab to move • Enter to proceed • Esc to go back"))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 64)).Render(body),
	)
}

func (m model) viewCACertPrompt(w int) string {
	title := styleTitle.Render("Custom CA Certificate")
	subtitle := styleMuted.Render("Required for air-gapped deployments with a private CA.")

	options := []string{"No", "Yes"}
	var parts []string
	for i, o := range options {
		if i == m.hasCACert {
			parts = append(parts, styleSelected.Render(" "+o+" "))
		} else {
			parts = append(parts, styleMuted.Render(o))
		}
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		title, subtitle, "", styleLabel.Render("Install a custom CA certificate?"),
		"", strings.Join(parts, "  "),
		"", styleHelp.Render("←/→ to select • Enter to proceed"),
	)
	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 64)).Render(body),
	)
}

func (m model) viewCACert(w int) string {
	title := styleTitle.Render("CA Certificate (PEM)")
	subtitle := styleMuted.Render("Paste the full PEM certificate block.")

	var lines []string
	lines = append(lines, title, subtitle, "", m.caCertInput.View())
	if m.validationErr != "" {
		lines = append(lines, "", styleError.Render(m.validationErr))
	}
	lines = append(lines, styleHelp.Render("Enter to confirm • Esc to go back"))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 64)).Render(body),
	)
}

func (m model) viewConfirm(w int) string {
	title := styleTitle.Render("Confirm Installation")

	cfg := m.buildConfig()
	role := "Initialize new cluster"
	if cfg.ClusterRole == "join" {
		role = fmt.Sprintf("Join cluster at %s", cfg.JoinAddr)
	}

	summary := []struct{ k, v string }{
		{"Disk", cfg.Disk},
		{"Management IP", cfg.ManagementIP},
		{"Subnet mask", cfg.SubnetMask},
		{"Gateway", cfg.Gateway},
		{"OVN interface", cfg.OVNInterface},
		{"Hostname", cfg.Hostname},
		{"Cluster role", role},
	}
	if cfg.HasCACert {
		summary = append(summary, struct{ k, v string }{"CA certificate", "provided"})
	}

	var rows []string
	for _, s := range summary {
		rows = append(rows, fmt.Sprintf("  %s%-20s%s  %s",
			styleLabel.Render(""), styleLabel.Render(s.k), "", s.v))
	}

	warning := styleWarning.Render("This will erase " + cfg.Disk + " and begin installation.")

	body := lipgloss.JoinVertical(lipgloss.Left,
		title, "",
		strings.Join(rows, "\n"), "",
		warning, "",
		styleHelp.Render("Enter/Y to install • N/Esc to cancel"),
	)
	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 72)).Render(body),
	)
}

func (m model) viewDone(w int) string {
	body := lipgloss.JoinVertical(lipgloss.Center,
		styleSuccess.Render("Installation complete."),
		"",
		styleMuted.Render("The system will reboot shortly."),
	)
	return lipgloss.Place(w, m.height, lipgloss.Center, lipgloss.Center,
		styleBox.Width(min(w-4, 48)).Render(body),
	)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (m model) buildConfig() *install.Config {
	cfg := &install.Config{}
	if len(m.disks) > m.diskCursor {
		cfg.Disk = m.disks[m.diskCursor].Path
	}
	cfg.ManagementIP = strings.TrimSpace(m.netInputs[fieldIP].Value())
	cfg.SubnetMask = strings.TrimSpace(m.netInputs[fieldMask].Value())
	cfg.Gateway = strings.TrimSpace(m.netInputs[fieldGateway].Value())
	if len(m.nics) > m.nicCursor {
		cfg.OVNInterface = m.nics[m.nicCursor]
	} else {
		cfg.OVNInterface = strings.TrimSpace(m.nicManualInput.Value())
	}
	cfg.Hostname = strings.TrimSpace(m.hostnameInput.Value())
	if m.clusterRole == 0 {
		cfg.ClusterRole = "init"
	} else {
		cfg.ClusterRole = "join"
		port := strings.TrimSpace(m.joinPortInput.Value())
		if port == "" {
			port = "4432"
		}
		cfg.JoinAddr = net.JoinHostPort(strings.TrimSpace(m.joinIPInput.Value()), port)
	}
	cfg.HasCACert = m.hasCACert == 1
	cfg.CACert = strings.TrimSpace(m.caCertInput.Value())
	cfg.RootPassword = m.passwordInput.Value()
	return cfg
}

// diskInfo holds display info for a block device.
type diskInfo struct {
	Path  string
	Size  string
	Model string
}

func availableDisks() ([]diskInfo, error) {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}
	var disks []diskInfo
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}
		d := diskInfo{Path: "/dev/" + name}
		d.Size = readSysBlockFile(name, "size")
		if d.Size != "" {
			// Convert 512-byte sectors to human-readable
			d.Size = formatSectors(d.Size)
		}
		d.Model = strings.TrimSpace(readSysBlockFile(name, "device/model"))
		disks = append(disks, d)
	}
	return disks, nil
}

func readSysBlockFile(dev, file string) string {
	data, err := os.ReadFile("/sys/block/" + dev + "/" + file)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func formatSectors(sectors string) string {
	var n int64
	if _, err := fmt.Sscan(sectors, &n); err != nil {
		return ""
	}
	bytes := n * 512
	switch {
	case bytes >= 1<<40:
		return fmt.Sprintf("%.1fT", float64(bytes)/(1<<40))
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(bytes)/(1<<20))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// validSubnetMask accepts dotted-decimal (255.255.255.0) or CIDR prefix (/24 or 24).
func validSubnetMask(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	// CIDR prefix form: 0–32
	var prefix int
	if _, err := fmt.Sscan(s, &prefix); err == nil && len(s) <= 2 {
		return prefix >= 0 && prefix <= 32
	}
	// Dotted-decimal form
	return net.ParseIP(s) != nil
}

func availableNICs() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var nics []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		nics = append(nics, iface.Name)
	}
	return nics, nil
}
