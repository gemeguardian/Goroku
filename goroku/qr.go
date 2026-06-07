package goroku

import (
	"fmt"

	"github.com/skip2/go-qrcode"
)

type QRCode struct {
	data string
}

func NewQRCode() *QRCode {
	return &QRCode{}
}

func (q *QRCode) AddData(data string) {
	q.data = data
}

func (q *QRCode) PrintASCII(invert bool) {
	if q.data == "" {
		fmt.Println("No QR code data specified")
		return
	}

	qr, err := qrcode.New(q.data, qrcode.Medium)
	if err != nil {
		fmt.Printf("Failed to generate QR code: %v\n", err)
		return
	}

	// Print small ASCII representation to stdout (mirroring print_ascii logic)
	fmt.Println(qr.ToSmallString(invert))
}
