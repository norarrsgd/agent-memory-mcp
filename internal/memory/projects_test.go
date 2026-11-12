package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

func TestProjectsPaginationErrorsIdentifyField(t *testing.T) {
	svc, _ := setup(t)
	for _, tc := range []struct {
		name  string
		in    domain.ProjectsInput
		field string
	}{
		{"negative limit", domain.ProjectsInput{Limit: -1}, "limit"},
		{"excessive limit", domain.ProjectsInput{Limit: 21}, "limit"},
		{"invalid cursor", domain.ProjectsInput{After: "/tmp/project"}, "after"},
		{"oversized cursor", domain.ProjectsInput{After: strings.Repeat("a", 81)}, "after"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Projects(context.Background(), tc.in)
			var failure *domain.Error
			if !errors.As(err, &failure) || failure.Code != "validation" || !strings.Contains(failure.Message, tc.field) {
				t.Fatalf("expected actionable %s validation error, got %v", tc.field, err)
			}
		})
	}
}
