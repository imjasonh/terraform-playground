package sshserver

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/display"
	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
)

type Session struct {
	AlertsClient *mta.Client
	TripClient   *mta.TripClient
	RefreshEvery time.Duration

	out    io.Writer
	in     io.Reader
	width  int
	height int

	mu             sync.Mutex
	selectedRoute  string
	renderNow      chan struct{}
	widthCh          chan int
}

func (s *Session) Run() {
	ticker := time.NewTicker(s.RefreshEvery)
	defer ticker.Stop()

	go s.readInput()

	s.paint()

	for {
		select {
		case <-ticker.C:
			s.paint()
		case <-s.renderNow:
			s.paint()
		case w := <-s.widthCh:
			s.mu.Lock()
			s.width = w
			s.mu.Unlock()
			s.paint()
		}
	}
}

func (s *Session) paint() {
	s.mu.Lock()
	width := s.width
	route := s.selectedRoute
	s.mu.Unlock()

	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	feed, err := s.AlertsClient.Fetch(ctx)
	if err != nil {
		s.writef("\x1b[2J\x1b[H\x1b[31mError fetching alerts: %v\x1b[0m", err)
		return
	}

	if route == "" {
		s.write(display.RenderOverview(feed, now, width, int(s.RefreshEvery.Seconds())))
		return
	}

	service := mta.LineStatus{RouteID: route, Status: mta.GoodService}
	for _, line := range mta.LineStatuses(feed, now) {
		if line.RouteID == route {
			service = line
			break
		}
	}

	if msg := mta.ShuttleDetailMessage(route); msg != "" {
		s.write(display.RenderLineDetail(route, service, nil, now, width, int(s.RefreshEvery.Seconds()), nil))
		return
	}

	tripFeed, tripErr := s.TripClient.FetchRoute(ctx, route)
	var activities []mta.StationActivity
	if tripErr == nil {
		activities, tripErr = mta.StationActivityForRoute(tripFeed, route, now)
	}
	s.write(display.RenderLineDetail(route, service, activities, now, width, int(s.RefreshEvery.Seconds()), tripErr))
}

func (s *Session) write(screen string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = io.WriteString(s.out, screen)
}

func (s *Session) writef(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.out, format, args...)
}

func (s *Session) readInput() {
	buf := make([]byte, 32)
	for {
		n, err := s.in.Read(buf)
		if err != nil || n == 0 {
			return
		}
		i := 0
		for i < n {
			if buf[i] == 0x1b && i+1 < n {
				if buf[i+1] == '[' {
					i += 2
					for i < n && buf[i] >= 0x40 && buf[i] <= 0x7e {
						i++
					}
					continue
				}
				s.clearSelection()
				i++
				continue
			}
			s.handleKey(buf[i])
			i++
		}
	}
}

func (s *Session) clearSelection() {
	s.mu.Lock()
	s.selectedRoute = ""
	s.mu.Unlock()
	s.triggerRender()
}

func (s *Session) handleKey(key byte) {
	switch key {
	case 0x1b: // escape sequence start — skip simple parsing
		return
	case 0x7f, 0x08: // backspace
		s.clearSelection()
	case 'q', 'Q':
		s.clearSelection()
	default:
		if route, ok := mta.RouteFromKey(key); ok {
			s.mu.Lock()
			s.selectedRoute = route
			s.mu.Unlock()
			s.triggerRender()
		}
	}
}

func (s *Session) triggerRender() {
	select {
	case s.renderNow <- struct{}{}:
	default:
	}
}

func (s *Session) SetWidth(width int) {
	select {
	case s.widthCh <- width:
	default:
	}
}
