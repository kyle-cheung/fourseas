package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/app"
	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

type fakeReconnectService struct {
	steps            []string
	authErr, syncErr error
}

func (f *fakeReconnectService) Reconnect(_ context.Context, id string, report app.Progress) (app.LinkedItem, error) {
	f.steps = append(f.steps, "reconnect:"+id)
	report("Open http://localhost:8080 in your browser")
	if f.authErr != nil {
		return app.LinkedItem{}, f.authErr
	}
	return app.LinkedItem{ItemID: id, Institution: "Wealthsimple"}, f.authErr
}

func (f *fakeReconnectService) SyncItem(_ context.Context, id string, _ app.Progress) ([]model.AccountView, error) {
	f.steps = append(f.steps, "sync:"+id)
	return nil, f.syncErr
}

func TestReconnectCommand(t *testing.T) {
	for _, scenario := range []string{"success", "cancel", "sync failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := &fakeReconnectService{}
			if scenario == "cancel" {
				f.authErr = errors.Join(context.Canceled, errors.New("link was closed before the bank sign-in finished"))
			}
			if scenario == "sync failure" {
				f.syncErr = errors.New("network unavailable")
			}
			var out bytes.Buffer
			err := runReconnectWith(context.Background(), f, []string{"item-1"}, &out)
			if scenario == "cancel" {
				if !errors.Is(err, context.Canceled) || len(f.steps) != 1 || err.Error() != "bank sign-in canceled; no sync was started" {
					t.Fatalf("steps=%v err=%v", f.steps, err)
				}
				return
			}
			if strings.Join(f.steps, ",") != "reconnect:item-1,sync:item-1" {
				t.Fatalf("steps=%v", f.steps)
			}
			if scenario == "sync failure" {
				if !errors.Is(err, f.syncErr) || !strings.Contains(err.Error(), "sign-in completed") || !strings.Contains(err.Error(), "fourseas sync") {
					t.Fatalf("error=%v", err)
				}
			} else if err != nil || !strings.Contains(out.String(), "Reconnected Wealthsimple") {
				t.Fatalf("output=%s err=%v", out.String(), err)
			}
		})
	}
}

func TestReconnectCommandRequiresOneItemID(t *testing.T) {
	for _, args := range [][]string{nil, {""}, {" "}, {"--help"}, {"one", "two"}} {
		f := &fakeReconnectService{}
		var out bytes.Buffer
		if err := runReconnectWith(context.Background(), f, args, &out); err == nil {
			t.Fatalf("accepted %q", args)
		}
		if len(f.steps) != 0 {
			t.Fatal("invalid arguments started reconnect")
		}
	}
}

func TestSyncReconnectHelpNamesEachBrokenItem(t *testing.T) {
	var out bytes.Buffer
	printSyncReconnectHelp(&out, []app.SyncResult{
		{ItemID: "item-1", Label: "Wealthsimple", Err: app.ErrLoginRequired},
		{ItemID: "item-2", Label: "Wealthsimple", Err: app.ErrLoginRequired},
		{ItemID: "item-3", Label: "Healthy"},
	})
	for _, id := range []string{"item-1", "item-2"} {
		if !strings.Contains(out.String(), "fourseas reconnect "+id) {
			t.Fatalf("missing command: %s", out.String())
		}
	}
	if strings.Contains(out.String(), "item-3") {
		t.Fatal("healthy item marked for reconnect")
	}
}

func TestReconnectCommandDirectsActionableErrorsToUserAction(t *testing.T) {
	for _, tt := range []struct {
		cause   error
		command string
	}{
		{app.ErrLoginRequired, "fourseas reconnect item-1"},
		{app.ErrAdditionalConsentRequired, "fourseas accounts liabilities enable"},
	} {
		f := &fakeReconnectService{syncErr: tt.cause}
		var out bytes.Buffer
		err := runReconnectWith(context.Background(), f, []string{"item-1"}, &out)
		if !errors.Is(err, tt.cause) || !strings.Contains(err.Error(), tt.command) || strings.Contains(err.Error(), "`fourseas sync`") {
			t.Fatalf("incorrect action: %v", err)
		}
	}
}

func TestReconnectCommandStatusFailureDoesNotRepeatSignIn(t *testing.T) {
	for _, cause := range []error{errors.New("saving status failed"), context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			partial := &app.ReconnectStatusError{Item: app.LinkedItem{ItemID: "item-1", Institution: "Wealthsimple"}, Err: cause}
			f := &fakeReconnectService{authErr: fmt.Errorf("reconnect: %w", partial)}
			var out bytes.Buffer
			err := runReconnectWith(context.Background(), f, []string{"item-1"}, &out)
			var got *app.ReconnectStatusError
			if !errors.Is(err, cause) || !errors.As(err, &got) || !strings.Contains(err.Error(), "fourseas sync") || len(f.steps) != 1 {
				t.Fatalf("steps=%v err=%v", f.steps, err)
			}
		})
	}
}
