package kernel

import (
	"bytes"
	"math"
)

// TrayIcon 生成一个 32x32 的 ICO 图标（圆角渐变底 + 终端提示符号）。
func TrayIcon() []byte {
	const n = 32
	type pixel struct{ r, g, b, a uint8 }
	img := make([]pixel, n*n)

	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			d := roundedRect(float64(x)+0.5, float64(y)+0.5, 2, 2, 28, 28, 8)
			cover := clamp(0.5-d, 0, 1)
			if cover <= 0.01 {
				continue
			}
			t := float64(y) / float64(n)
			r := lerp(79, 14, t)
			g := lerp(70, 165, t)
			b := lerp(229, 233, t)

			chevron := math.Min(
				segmentDist(float64(x)+0.5, float64(y)+0.5, 10, 10, 19, 16),
				segmentDist(float64(x)+0.5, float64(y)+0.5, 19, 16, 10, 21),
			)
			bar := segmentDist(float64(x)+0.5, float64(y)+0.5, 18, 24, 24, 24)
			stroke := math.Min(chevron, bar)
			if stroke < 1.4 {
				gc := clamp(1.4-stroke, 0, 1)
				r = lerp(r, 255, gc)
				g = lerp(g, 255, gc)
				b = lerp(b, 255, gc)
			}

			img[y*n+x] = pixel{uint8(r), uint8(g), uint8(b), uint8(cover * 255)}
		}
	}

	// ICO = ICONDIR + ICONDIRENTRY + BITMAPINFOHEADER + XOR + AND
	const header = 40
	xorSize := n * n * 4
	andRow := ((n + 31) / 32) * 4
	andSize := andRow * n

	var buf bytes.Buffer
	// ICONDIR
	buf.Write([]byte{0, 0, 1, 0, 1, 0})
	// ICONDIRENTRY
	buf.WriteByte(n)
	buf.WriteByte(n)
	buf.WriteByte(0) // color count
	buf.WriteByte(0)
	writeLE16(&buf, 1) // planes
	writeLE16(&buf, 32)
	writeLE32(&buf, uint32(header+xorSize+andSize))
	writeLE32(&buf, 22)
	// BITMAPINFOHEADER
	writeLE32(&buf, header)
	writeLE32(&buf, n)
	writeLE32(&buf, n*2)
	writeLE16(&buf, 1)
	writeLE16(&buf, 32)
	writeLE32(&buf, 0)
	writeLE32(&buf, uint32(xorSize+andSize))
	writeLE32(&buf, 0)
	writeLE32(&buf, 0)
	writeLE32(&buf, 0)
	writeLE32(&buf, 0)

	// XOR 数据自下而上
	for y := n - 1; y >= 0; y-- {
		for x := 0; x < n; x++ {
			p := img[y*n+x]
			buf.Write([]byte{p.b, p.g, p.r, p.a})
		}
	}
	// AND 掩码
	for i := 0; i < andSize; i++ {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

func writeLE16(b *bytes.Buffer, v uint16) {
	b.Write([]byte{byte(v), byte(v >> 8)})
}

func writeLE32(b *bytes.Buffer, v uint32) {
	b.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}

func clamp(v, lo, hi float64) float64 {
	return math.Min(hi, math.Max(lo, v))
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

// segmentDist 返回到线段的距离。
func segmentDist(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	if dx == 0 && dy == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / (dx*dx + dy*dy)
	t = clamp(t, 0, 1)
	cx, cy := x1+t*dx, y1+t*dy
	return math.Hypot(px-cx, py-cy)
}

// roundedRect 返回有符号距离，负数表示在矩形内部。
func roundedRect(px, py, x, y, w, h, r float64) float64 {
	cx, cy := x+w/2, y+h/2
	qx := math.Abs(px-cx) - (w/2 - r)
	qy := math.Abs(py-cy) - (h/2 - r)
	outside := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
	inside := math.Min(math.Max(qx, qy), 0)
	return outside + inside - r
}
