package booking

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"banya.local/backend/assets/fonts"
	"banya.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/signintech/gopdf"
)

const ticketWarning = "Тестовый билет: не является чеком, подтверждением реальной оплаты или разрешением на фактический проход."

type Ticket struct {
	OrderID  string    `json:"order_id"`
	Status   string    `json:"status"`
	PaidAt   time.Time `json:"paid_at"`
	TestMode bool      `json:"test_mode"`
	Warning  string    `json:"warning"`
	Visit    Quote     `json:"visit"`
}

func (s *Service) loadTicket(ctx context.Context, token string) (Ticket, error) {
	t := Ticket{Status: "confirmed", TestMode: true, Warning: ticketWarning}
	if len(token) != 64 {
		return t, platform.Err(404, "not_found", "Билет не найден")
	}
	err := s.DB.QueryRow(ctx, "SELECT id::text,quote,confirmed_at FROM orders WHERE ticket_hash=$1 AND status='confirmed' AND ticket_expires_at>clock_timestamp()", platform.Hash(token)).Scan(&t.OrderID, &t.Visit, &t.PaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.Err(404, "not_found", "Билет не найден")
	}
	return t, err
}

type ticketLayout struct {
	pdf    gopdf.GoPdf
	y      float64
	page   int
	ctx    context.Context
	widths map[string]map[rune]float64
}

func (p *ticketLayout) footer() error {
	if err := p.pdf.SetFont("text", "", 9); err != nil {
		return err
	}
	p.pdf.SetTextColor(100, 100, 100)
	p.pdf.SetXY(42, 806)
	return p.pdf.Cell(nil, fmt.Sprintf("Тестовый режим | Не является чеком или пропуском | Страница %d", p.page))
}
func (p *ticketLayout) newPage() error {
	if p.page > 0 {
		if err := p.footer(); err != nil {
			return err
		}
	}
	p.pdf.AddPage()
	p.page++
	p.y = 44
	return nil
}
func (p *ticketLayout) write(text string, size int, bold bool) error {
	font := "text"
	if bold {
		font = "bold"
	}
	if err := p.pdf.SetFont(font, "", size); err != nil {
		return err
	}
	p.pdf.SetTextColor(32, 43, 53)
	key := fmt.Sprintf("%s:%d", font, size)
	if p.widths == nil {
		p.widths = map[string]map[rune]float64{}
	}
	widths := p.widths[key]
	if widths == nil {
		widths = map[rune]float64{}
		p.widths[key] = widths
	}
	line := make([]rune, 0, 128)
	measures := make([]float64, 0, 128)
	total := float64(0)
	flush := func() error {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		if p.y+float64(size)+6 > 775 {
			if err := p.newPage(); err != nil {
				return err
			}
			if err := p.pdf.SetFont(font, "", size); err != nil {
				return err
			}
			p.pdf.SetTextColor(32, 43, 53)
		}
		p.pdf.SetXY(42, p.y)
		if err := p.pdf.Cell(nil, string(line)); err != nil {
			return err
		}
		p.y += float64(size) + 6
		return nil
	}
	for _, r := range text {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		if r == '\n' {
			if err := flush(); err != nil {
				return err
			}
			line = line[:0]
			measures = measures[:0]
			total = 0
			continue
		}
		width, ok := widths[r]
		if !ok {
			var err error
			width, err = p.pdf.MeasureTextWidth(string(r))
			if err != nil {
				return err
			}
			if len(widths) < 4096 {
				widths[r] = width
			}
		}
		if total+width > 510 && len(line) > 0 {
			split := -1
			for i := len(line) - 1; i > 0; i-- {
				if line[i] == ' ' {
					split = i
					break
				}
			}
			if split > 0 {
				tail := append([]rune(nil), line[split+1:]...)
				tailWidths := append([]float64(nil), measures[split+1:]...)
				line = line[:split]
				if err := flush(); err != nil {
					return err
				}
				line = append(line[:0], tail...)
				measures = append(measures[:0], tailWidths...)
				total = 0
				for _, v := range measures {
					total += v
				}
			} else {
				if err := flush(); err != nil {
					return err
				}
				line = line[:0]
				measures = measures[:0]
				total = 0
			}
		}
		if r == ' ' && len(line) == 0 {
			continue
		}
		line = append(line, r)
		measures = append(measures, width)
		total += width
	}
	if len(line) > 0 || text == "" {
		return flush()
	}
	return nil
}

func ticketPDF(t Ticket) ([]byte, error) { return ticketPDFContext(context.Background(), t) }
func ticketPDFContext(ctx context.Context, t Ticket) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := ticketLayout{ctx: ctx}
	p.pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	p.pdf.SetNoCompression()
	if err := p.pdf.AddTTFFontData("text", fonts.Regular); err != nil {
		return nil, err
	}
	if err := p.pdf.AddTTFFontData("bold", fonts.Bold); err != nil {
		return nil, err
	}
	if err := p.newPage(); err != nil {
		return nil, err
	}
	type row struct {
		text string
		size int
		bold bool
	}
	q := t.Visit
	rows := []row{{"САНДУНОВСКИЕ БАНИ", 20, true}, {"ТЕСТОВЫЙ ЭЛЕКТРОННЫЙ БИЛЕТ", 14, true}, {"Заказ: " + t.OrderID, 10, false}, {"", 6, false}, {q.BathName, 14, true}, {q.BathAddress, 11, false}, {q.DepartmentName, 12, true}, {"Начало: " + q.StartsAt.In(Location).Format("02.01.2006 15:04") + " (Екатеринбург)", 11, false}, {"Окончание: " + q.EndsAt.In(Location).Format("02.01.2006 15:04"), 11, false}, {"", 6, false}}
	labels := make([]string, 0, len(q.Resources))
	for _, r := range q.Resources {
		labels = append(labels, r.Label)
	}
	resourceTitle := "Места: "
	if q.Mode == "sauna" {
		resourceTitle = "Сауна целиком: "
	}
	rows = append(rows, row{resourceTitle + strings.Join(labels, ", "), 11, true}, row{"СОСТАВ ГОСТЕЙ", 12, true})
	for _, g := range q.Guests {
		label := fmt.Sprintf("Взрослые: %d", g.Count)
		if g.Age < 18 {
			label = fmt.Sprintf("Дети (полных лет: %d): %d", g.Age, g.Count)
		}
		rows = append(rows, row{label, 11, false})
	}
	rows = append(rows, row{"", 6, false}, row{"РАСЧЁТ СТОИМОСТИ", 12, true})
	for _, line := range q.Lines {
		text := fmt.Sprintf("%s; количество: %d; ставка: %s RUB", line.Description, line.Count, money(line.Rate))
		if q.Mode == "sauna" {
			text += fmt.Sprintf("/час; период: %d мин.", line.Minutes)
		}
		rows = append(rows, row{text, 10, false}, row{"Правило тарифа: " + line.TariffID, 9, false})
	}
	rows = append(rows, row{"", 6, false}, row{"Полная тестовая сумма: " + money(q.Amount) + " RUB", 14, true}, row{"Тестовая оплата подтверждена: " + t.PaidAt.In(Location).Format("02.01.2006 15:04"), 10, false}, row{"", 6, false}, row{ticketWarning, 11, true}, row{"Ребёнок без отдельного места не получает дополнительный номер места.", 10, false})
	for _, r := range rows {
		if err := p.write(r.text, r.size, r.bold); err != nil {
			return nil, err
		}
	}
	if err := p.footer(); err != nil {
		return nil, err
	}
	return p.pdf.GetBytesPdfReturnErr()
}
func money(value int64) string { return fmt.Sprintf("%d,%02d", value/100, value%100) }
func (s *Service) downloadTicket(w http.ResponseWriter, r *http.Request) {
	t, err := s.loadTicket(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	data, err := s.cachedPDF(r.Context(), t)
	if err != nil {
		w.Header().Set("Retry-After", "1")
		platform.Fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="ticket-`+t.OrderID+`.pdf"`)
	http.ServeContent(w, r, "ticket.pdf", t.PaidAt, bytes.NewReader(data))
}
