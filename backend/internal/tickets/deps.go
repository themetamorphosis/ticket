package tickets

// Keep the pinned PDF and QR libraries in go.mod until the renderer uses them.
import (
	_ "github.com/signintech/gopdf"
	_ "github.com/skip2/go-qrcode"
)
