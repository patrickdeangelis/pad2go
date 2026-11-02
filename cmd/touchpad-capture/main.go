// Command touchpad-capture records the visible desktop and the DSU samples
// published by Pad2Go. It is a DSU client and never opens the controller HID.
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	dsuHeaderSize = 16
	dsuVersion    = 1001
	msgPadData    = 0x100002
	padBodySize   = 84
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:26760", "Pad2Go DSU server address")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "ffmpeg executable (must include gdigrab)")
	duration := flag.Duration("duration", 60*time.Second, "capture duration; Ctrl+C also stops cleanly")
	out := flag.String("out", "eden-touchpad-capture", "output path without extension; creates .mp4 and .csv")
	flag.Parse()
	if runtime.GOOS != "windows" {
		return errors.New("desktop capture currently requires Windows and FFmpeg gdigrab")
	}
	if *duration <= 0 {
		return errors.New("duration must be positive")
	}
	if strings.TrimSpace(*out) == "" {
		return errors.New("output path must not be empty")
	}

	base := strings.TrimSuffix(*out, filepath.Ext(*out))
	videoPath, csvPath := base+".mp4", base+".csv"
	if err := os.MkdirAll(filepath.Dir(videoPath), 0o755); err != nil && filepath.Dir(videoPath) != "." {
		return fmt.Errorf("create output directory: %w", err)
	}
	video, err := os.Create(videoPath)
	if err != nil {
		return fmt.Errorf("create video: %w", err)
	}
	_ = video.Close()
	log, err := os.Create(csvPath)
	if err != nil {
		return fmt.Errorf("create DSU log: %w", err)
	}
	defer log.Close()

	conn, err := net.Dial("udp", *addr)
	if err != nil {
		return fmt.Errorf("connect to Pad2Go DSU at %s: %w", *addr, err)
	}
	defer conn.Close()

	w := csvWriter{writer: bufio.NewWriter(log)}
	cmd, err := startDesktopCapture(*ffmpeg, videoPath)
	if err != nil {
		return err
	}
	started := cmd.startedAt
	defer func() {
		if cmd.stdin != nil {
			_, _ = io.WriteString(cmd.stdin, "q\n")
			_ = cmd.stdin.Close()
		}
		if cmd.cmd.Process != nil {
			_ = cmd.cmd.Process.Kill()
		}
	}()
	if _, err := fmt.Fprintf(log, "# video_start_utc=%s\n# dsu_address=%s\n# video=%s\n", started.UTC().Format(time.RFC3339Nano), *addr, videoPath); err != nil {
		return err
	}
	if err := w.write([]string{"receive_utc", "elapsed_ms", "pad", "counter", "touch1_active", "touch1_id", "touch1_x", "touch1_y", "touch2_active", "touch2_id", "touch2_x", "touch2_y", "gyro_pitch_dps", "gyro_yaw_dps", "gyro_roll_dps"}); err != nil {
		return err
	}
	if err := w.flush(); err != nil {
		return err
	}

	fmt.Printf("Gravando desktop e DSU; não acessa o HID.\nVídeo: %s\nDados: %s\nFaça swipes horizontal e vertical no seletor do Galaxy. Ctrl+C encerra.\n", videoPath, csvPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deadline := time.NewTimer(*duration)
	defer deadline.Stop()
	refresh := time.NewTicker(2 * time.Second)
	defer refresh.Stop()
	if err := sendSubscribeAll(conn); err != nil {
		return fmt.Errorf("subscribe to Pad2Go DSU: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 512)
	rows := 0
	for {
		select {
		case <-ctx.Done():
			goto finish
		case <-deadline.C:
			goto finish
		case <-refresh.C:
			if err := sendSubscribeAll(conn); err != nil {
				return fmt.Errorf("refresh DSU subscription: %w", err)
			}
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, err := conn.Read(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if captureErr := cmd.poll(); captureErr != nil {
					return captureErr
				}
				continue
			}
			return fmt.Errorf("read DSU packet: %w", err)
		}
		record, ok := parsePadData(buf[:n])
		if !ok {
			continue
		}
		now := time.Now()
		if err := w.write(record.csvRow(now, started)); err != nil {
			return fmt.Errorf("write DSU data: %w", err)
		}
		rows++
	}

finish:
	if err := w.flush(); err != nil {
		return err
	}
	if err := cmd.stop(); err != nil {
		return fmt.Errorf("finish desktop video: %w", err)
	}
	info, err := os.Stat(videoPath)
	if err != nil {
		return fmt.Errorf("video was not created: %w", err)
	}
	if info.Size() == 0 {
		return errors.New("FFmpeg produced an empty video; check that the desktop is visible and gdigrab is available")
	}
	fmt.Printf("Captura finalizada: %d DSU amostras, vídeo %d bytes. Envie os dois arquivos para medir.\n", rows, info.Size())
	return nil
}

type desktopCapture struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	done      chan error
	result    error
	finished  bool
	startedAt time.Time
}

func (c *desktopCapture) poll() error {
	select {
	case err := <-c.done:
		c.result, c.finished = err, true
		if err == nil {
			return errors.New("FFmpeg stopped during capture")
		}
		return fmt.Errorf("FFmpeg stopped during capture: %w", err)
	default:
		return nil
	}
}

func (c *desktopCapture) stop() error {
	if c.stdin != nil {
		_, _ = io.WriteString(c.stdin, "q\n")
		_ = c.stdin.Close()
		c.stdin = nil
	}
	if c.done != nil && !c.finished {
		c.result = <-c.done
		c.finished = true
	}
	return c.result
}

func startDesktopCapture(ffmpeg, output string) (*desktopCapture, error) {
	cmd := exec.Command(ffmpeg,
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "gdigrab", "-framerate", "30", "-draw_mouse", "1", "-i", "desktop",
		"-an", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "20", "-pix_fmt", "yuv420p",
		"-movflags", "+faststart", output,
	)
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open FFmpeg input: %w", err)
	}
	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start FFmpeg desktop capture: %w", err)
	}
	capture := &desktopCapture{cmd: cmd, stdin: stdin, done: make(chan error, 1), startedAt: startedAt}
	go func() { capture.done <- cmd.Wait() }()
	time.Sleep(150 * time.Millisecond)
	if err := capture.poll(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	return capture, nil
}

func sendSubscribeAll(conn net.Conn) error {
	payload := make([]byte, 12)
	binary.LittleEndian.PutUint32(payload, msgPadData)
	packet := make([]byte, dsuHeaderSize+len(payload))
	copy(packet, "DSUC")
	binary.LittleEndian.PutUint16(packet[4:], dsuVersion)
	binary.LittleEndian.PutUint16(packet[6:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(packet[12:], 0x50414432)
	copy(packet[dsuHeaderSize:], payload)
	binary.LittleEndian.PutUint32(packet[8:], crc32.ChecksumIEEE(packet))
	_, err := conn.Write(packet)
	return err
}

type padSample struct {
	pad, counter   uint32
	touch1, touch2 contactSample
	gyro           [3]float32
}

type contactSample struct {
	active byte
	id     byte
	x, y   uint16
}

func parsePadData(packet []byte) (padSample, bool) {
	var out padSample
	if len(packet) < dsuHeaderSize+padBodySize || string(packet[:4]) != "DSUS" || binary.LittleEndian.Uint16(packet[4:6]) != dsuVersion {
		return out, false
	}
	length := int(binary.LittleEndian.Uint16(packet[6:8]))
	if length != padBodySize || len(packet) < dsuHeaderSize+length {
		return out, false
	}
	copyPacket := append([]byte(nil), packet[:dsuHeaderSize+length]...)
	want := binary.LittleEndian.Uint32(copyPacket[8:12])
	clear(copyPacket[8:12])
	if crc32.ChecksumIEEE(copyPacket) != want {
		return out, false
	}
	body := packet[dsuHeaderSize : dsuHeaderSize+length]
	if binary.LittleEndian.Uint32(body[:4]) != msgPadData {
		return out, false
	}
	out.pad, out.counter = uint32(body[4]), binary.LittleEndian.Uint32(body[16:20])
	out.touch1 = contactSample{active: body[40], id: body[41], x: binary.LittleEndian.Uint16(body[42:44]), y: binary.LittleEndian.Uint16(body[44:46])}
	out.touch2 = contactSample{active: body[46], id: body[47], x: binary.LittleEndian.Uint16(body[48:50]), y: binary.LittleEndian.Uint16(body[50:52])}
	for i := range out.gyro {
		out.gyro[i] = math.Float32frombits(binary.LittleEndian.Uint32(body[72+i*4 : 76+i*4]))
	}
	return out, true
}

func (s padSample) csvRow(now, started time.Time) []string {
	return []string{
		now.UTC().Format(time.RFC3339Nano), fmt.Sprintf("%.3f", float64(now.Sub(started).Microseconds())/1000),
		fmt.Sprint(s.pad), fmt.Sprint(s.counter), fmt.Sprint(s.touch1.active), fmt.Sprint(s.touch1.id), fmt.Sprint(s.touch1.x), fmt.Sprint(s.touch1.y),
		fmt.Sprint(s.touch2.active), fmt.Sprint(s.touch2.id), fmt.Sprint(s.touch2.x), fmt.Sprint(s.touch2.y),
		fmt.Sprintf("%.6f", s.gyro[0]), fmt.Sprintf("%.6f", s.gyro[1]), fmt.Sprintf("%.6f", s.gyro[2]),
	}
}

type csvWriter struct{ writer *bufio.Writer }

func (w csvWriter) write(fields []string) error {
	for i, field := range fields {
		if i > 0 {
			if err := w.writer.WriteByte(','); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w.writer, field); err != nil {
			return err
		}
	}
	return w.writer.WriteByte('\n')
}

func (w csvWriter) flush() error { return w.writer.Flush() }
