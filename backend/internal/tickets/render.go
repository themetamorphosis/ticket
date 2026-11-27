package tickets

import (
	_ "embed"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // event time zones must not depend on the host's zoneinfo

	"github.com/signintech/gopdf"
	"github.com/skip2/go-qrcode"
)

var (
	//go:embed fonts/NotoSans-Regular.ttf
	fontRegular []byte
	//go:embed fonts/NotoSans-Bold.ttf
	fontBold []byte
)

// Doc is everything printed on one ticket. It deliberately has no buyer
// contact details.
type Doc struct {
	Organizer    string
	EventTitle   string
	StartsAt     time.Time
	TimeZone     string
	Venue        string
	TicketType   string
	Ordinal      int
	Of           int
	OrderRef     string
	FallbackCode string
	// QRContent is the staff scanner URL with the credential in its fragment.
	QRContent string
}

// FileName is the attachment name, e.g. "ticket-2-of-3.pdf".
func (d Doc) FileName() string { return fmt.Sprintf("ticket-%d-of-%d.pdf", d.Ordinal, d.Of) }

// pdf records the first error from a sequence of drawing calls.
type pdf struct {
	g   gopdf.GoPdf
	err error
}

func (p *pdf) check(err error) {
	if p.err == nil {
		p.err = err
	}
}

// text draws s left-aligned from y, wrapped to the page width, and returns
// the y below it so the layout flows when a long title wraps. Drawing stops
// at the first error: gopdf panics if asked to draw in a font that failed to load.
func (p *pdf) text(font string, size float64, gray uint8, y float64, s string) float64 {
	if p.err != nil {
		return y
	}
	p.check(p.g.SetFont(font, "", size))
	p.g.SetTextColor(gray, gray, gray)
	lines, err := p.g.SplitText(s, pageW-2*margin)
	p.check(err)
	lh := size * 1.35
	for _, line := range lines {
		p.g.SetXY(margin, y)
		p.check(p.g.Cell(&gopdf.Rect{W: pageW - 2*margin, H: lh}, strings.TrimSpace(line)))
		y += lh
	}
	return y
}

func (p *pdf) centered(font string, size float64, y float64, s string) {
	if p.err != nil {
		return
	}
	p.check(p.g.SetFont(font, "", size))
	p.g.SetTextColor(0, 0, 0)
	p.g.SetXY(margin, y)
	p.check(p.g.CellWithOption(&gopdf.Rect{W: pageW - 2*margin, H: size * 1.3}, s, gopdf.CellOption{Align: gopdf.Center}))
}

func (p *pdf) image(qr *qrcode.QRCode, x, y, size float64) {
	if p.err != nil {
		return
	}
	p.check(p.g.ImageFrom(qr.Image(1024), x, y, &gopdf.Rect{W: size, H: size}))
}

const (
	pageW  = 420.0 // A5 in points
	margin = 32.0
	qrSize = 230.0
)

// Render draws one ticket as a single A5 page: event details, the ticket's
// place in the order, a large high-contrast QR code with its quiet zone, and
// the fallback code for typed entry.
func Render(d Doc) ([]byte, error) {
	loc, err := time.LoadLocation(d.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("tickets: time zone %q: %w", d.TimeZone, err)
	}
	// Medium error correction survives smudges and low screen brightness;
	// the default 4-module border is the quiet zone scanners need.
	qr, err := qrcode.New(d.QRContent, qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("tickets: qr: %w", err)
	}

	p := &pdf{}
	p.g.Start(gopdf.Config{PageSize: *gopdf.PageSizeA5})
	p.g.SetInfo(gopdf.PdfInfo{Title: d.EventTitle + " ticket " + fmt.Sprint(d.Ordinal), Creator: "Kacheem Comms"})
	p.g.AddPage()
	p.check(p.g.AddTTFFontData("regular", fontRegular))
	p.check(p.g.AddTTFFontData("bold", fontBold))

	y := p.text("regular", 10, 90, 34, d.Organizer)
	y = p.text("bold", 20, 0, y+2, d.EventTitle)
	y = p.text("regular", 11, 0, y+6, d.StartsAt.In(loc).Format("Monday 2 January 2006, 15:04 MST"))
	y = p.text("regular", 11, 0, y, d.Venue)
	y = p.text("bold", 12, 0, y+8, fmt.Sprintf("%s · Ticket %d of %d", d.TicketType, d.Ordinal, d.Of))

	y += 10
	p.image(qr, (pageW-qrSize)/2, y, qrSize)
	y += qrSize + 8
	p.centered("regular", 9, y, "Code for manual entry")
	p.centered("bold", 17, y+13, d.FallbackCode)
	p.centered("regular", 9, y+40, "Order "+d.OrderRef)
	p.text("regular", 9, 70, y+64, "Show this code at the entrance. Each ticket admits one person, once. "+
		"Keep it private: the first person to present it is admitted.")

	if p.err != nil {
		return nil, fmt.Errorf("tickets: render: %w", p.err)
	}
	return p.g.GetBytesPdfReturnErr()
}
