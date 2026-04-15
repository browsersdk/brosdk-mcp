package stdio

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"brosdk-mcp/internal/mcp"
)

type Server struct {
	logger  *slog.Logger
	handler *mcp.Handler
	in      io.Reader
	out     io.Writer
}

func NewServer(logger *slog.Logger, handler *mcp.Handler) *Server {
	return &Server{
		logger:  logger,
		handler: handler,
		in:      os.Stdin,
		out:     os.Stdout,
	}
}

func (s *Server) SetIO(in io.Reader, out io.Writer) {
	s.in = in
	s.out = out
}

func (s *Server) Run(ctx context.Context) error {
	s.logger.Info("Waiting for MCP requests on stdio...")

	scanner := bufio.NewScanner(s.in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		req, err := mcp.ParseRequest([]byte(line))
		if err != nil {
			resp := mcp.Response{
				JSONRPC: "2.0",
				Error:   &mcp.Error{Code: -32700, Message: fmt.Sprintf("parse error: %v", err)},
			}
			if writeErr := s.writeResponse(resp); writeErr != nil {
				return writeErr
			}
			continue
		}

		resp := s.handler.HandleRequest(ctx, req)
		if mcp.IsNotification(req) {
			continue
		}
		if err := s.writeResponse(resp); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return nil
	default:
		return io.EOF
	}
}

func (s *Server) writeResponse(resp mcp.Response) error {
	raw, err := mcp.EncodeResponse(resp)
	if err != nil {
		return err
	}
	if _, err := s.out.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}
