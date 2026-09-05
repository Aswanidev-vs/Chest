package cli

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"

	"golang.org/x/term"
)

// ============================================================
// Palette
// ============================================================

var (
	// Warm off-white for the chest and CHEST wordmark
	chestWhite = color.RGBA{R: 236, G: 232, B: 220, A: 255}

	// Gold latch
	gold = color.RGBA{R: 245, G: 185, B: 15, A: 255}
)

// ANSI colors for the text tagline
const (
	ansiReset = "\x1b[0m"
	ansiGreen = "\x1b[38;2;74;212;66m"   // #4AD442
	ansiGray  = "\x1b[38;2;150;153;155m" // #96999B
)

// ============================================================
// Public
// ============================================================

// RenderChestLogo renders the pixel art logo followed by the ANSI tagline.
func RenderChestLogo() string {
	width := terminalWidth()
	if width < 45 {
		return renderTinyLogo()
	}

	logo := renderImage(buildLogoImage())
	tagline := renderTaglineText()

	return logo + "\n\n" + tagline
}

// ============================================================
// Logo Image (Only Chest + CHEST wordmark)
// ============================================================

func buildLogoImage() *image.RGBA {
	const (
		width  = 64
		height = 12 // Exactly 6 terminal rows
	)

	// All pixels are initialized with A=0 (fully transparent)
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// 1. Draw chest icon on the left
	drawChest(img, 1, 0)

	// 2. Draw "CHEST" wordmark vertically centered next to it
	drawWordmark(img, 24, 2)

	return img
}

// ============================================================
// Chest Icon
// ============================================================

func drawChest(img *image.RGBA, x, y int) {
	rows := []string{
		"   ############   ", // 0: Top lid curve
		" ##            ## ", // 1: Rounded corners
		"#                #", // 2: Side borders
		"#                #", // 3: Side borders
		"##################", // 4: Lid seam line
		"#      YYYY      #", // 5: Latch top
		"#      Y..Y      #", // 6: Latch hollow center
		"#      YYYY      #", // 7: Latch bottom
		"#                #", // 8: Lower body
		"##              ##", // 9: Lower corners
		" ################ ", // 10: Feet & base
	}

	for r, row := range rows {
		for c, ch := range row {
			switch ch {
			case '#':
				setPixel(img, x+c, y+r, chestWhite)
			case 'Y':
				setPixel(img, x+c, y+r, gold)
			}
		}
	}
}

// ============================================================
// CHEST Wordmark
// ============================================================

var chestLetters = map[byte][]string{
	'C': {
		" ####",
		"##   ",
		"##   ",
		"##   ",
		"##   ",
		"##   ",
		" ####",
	},
	'H': {
		"##  ##",
		"##  ##",
		"##  ##",
		"######",
		"##  ##",
		"##  ##",
		"##  ##",
	},
	'E': {
		"######",
		"##    ",
		"##    ",
		"##### ",
		"##    ",
		"##    ",
		"######",
	},
	'S': {
		" #### ",
		"##    ",
		"##    ",
		" #### ",
		"    ##",
		"    ##",
		" #### ",
	},
	'T': {
		"######",
		"  ##  ",
		"  ##  ",
		"  ##  ",
		"  ##  ",
		"  ##  ",
		"  ##  ",
	},
}

func drawWordmark(img *image.RGBA, startX, y int) {
	cursor := startX
	const gap = 2
	word := "CHEST"

	for i := 0; i < len(word); i++ {
		pattern := chestLetters[word[i]]
		for py, row := range pattern {
			for px, ch := range row {
				if ch == '#' {
					setPixel(img, cursor+px, y+py, chestWhite)
				}
			}
		}
		cursor += len(pattern[0]) + gap
	}
}

// ============================================================
// Native ANSI Text Tagline
// ============================================================

func renderTaglineText() string {
	prompt := ansiGreen + ">_" + ansiReset
	dot := ansiGreen + " • " + ansiReset

	parts := []string{"SORT", "INDEX", "FIND", "ORGANIZE"}
	var coloredParts []string
	for _, p := range parts {
		coloredParts = append(coloredParts, ansiGray+p+ansiReset)
	}

	return "  " + prompt + "  " + strings.Join(coloredParts, dot)
}

// ============================================================
// Transparent Half-Block Terminal Renderer
// ============================================================

func renderImage(img image.Image) string {
	bounds := img.Bounds()
	var out strings.Builder

	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			top := pixelAt(img, x, y)
			var bottom color.RGBA
			if y+1 < bounds.Max.Y {
				bottom = pixelAt(img, x, y+1)
			}
			out.WriteString(colorHalfBlock(top, bottom))
		}
		out.WriteString("\x1b[0m\n")
	}

	return strings.TrimRight(out.String(), "\n")
}

func pixelAt(img image.Image, x, y int) color.RGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.RGBA{
		R: uint8(r >> 8),
		G: uint8(g >> 8),
		B: uint8(b >> 8),
		A: uint8(a >> 8),
	}
}

// colorHalfBlock supports terminal background transparency
func colorHalfBlock(top, bottom color.RGBA) string {
	topOpaque := top.A > 0
	botOpaque := bottom.A > 0

	switch {
	case !topOpaque && !botOpaque:
		// Both transparent: natural terminal space
		return "\x1b[0m "

	case topOpaque && !botOpaque:
		// Top colored, bottom inherits terminal background
		return fmt.Sprintf("\x1b[0m\x1b[38;2;%d;%d;%dm▀", top.R, top.G, top.B)

	case !topOpaque && botOpaque:
		// Bottom colored, top inherits terminal background (using lower half-block ▄)
		return fmt.Sprintf("\x1b[0m\x1b[38;2;%d;%d;%dm▄", bottom.R, bottom.G, bottom.B)

	default:
		// Both colored
		return fmt.Sprintf(
			"\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
			top.R, top.G, top.B,
			bottom.R, bottom.G, bottom.B,
		)
	}
}

func setPixel(img *image.RGBA, x, y int, c color.Color) {
	if image.Pt(x, y).In(img.Bounds()) {
		img.Set(x, y, c)
	}
}

func renderTinyLogo() string {
	return strings.Join([]string{
		"  ▣ CHEST",
		renderTaglineText(),
	}, "\n")
}

func terminalWidth() int {
	fd := int(os.Stdout.Fd())
	width, _, err := term.GetSize(fd)
	if err != nil || width <= 0 {
		return 80
	}
	return width
}
