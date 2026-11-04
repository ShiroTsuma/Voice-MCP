package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRegistry(t *testing.T) {
	on := false
	r := &Registry{}
	r.Add(Integration{ID: "late", Group: "listening", Title: "Late"})
	r.Add(Integration{
		ID: "hooks", Group: "agents", Title: "Hooks",
		Status: func() Status {
			if on {
				return Status{State: On, Actions: []Action{{ID: "off", Label: "Turn off"}}}
			}
			return Status{State: Off, Actions: []Action{{ID: "on", Label: "Turn on", Primary: true}}}
		},
		Do: func(_ context.Context, action string, log func(string)) error {
			on = action == "on"
			log("done " + action)
			return nil
		},
	})
	release := make(chan struct{})
	r.Add(Integration{
		ID: "model", Group: "speech", Title: "Model",
		Status: func() Status { return Status{State: Missing, Actions: []Action{{ID: "download", Long: true}}} },
		Do: func(_ context.Context, _ string, log func(string)) error {
			log("50%")
			<-release
			return errors.New("network gone")
		},
	})

	list := r.List()
	if len(list) != 3 || list[0].ID != "hooks" || list[1].ID != "model" || list[2].ID != "late" {
		t.Fatalf("group order: %+v", list)
	}
	if list[2].Status.State != On {
		t.Fatalf("no status func means on: %+v", list[2].Status)
	}

	if _, err := r.Start("hooks", "off"); err == nil {
		t.Fatal("an action the status does not offer ran")
	}
	j, err := r.Start("hooks", "on")
	if err != nil || !j.Done || len(j.Log) != 1 || !on {
		t.Fatalf("short action: %+v %v on=%v", j, err, on)
	}

	j, err = r.Start("model", "download")
	if err != nil || j.Done {
		t.Fatalf("long action should run in the background: %+v %v", j, err)
	}
	if _, err := r.Start("model", "download"); err == nil {
		t.Fatal("a second action while one runs")
	}
	if v := r.List()[1]; v.Job == nil || v.Job.Done || len(v.Status.Actions) != 0 {
		t.Fatalf("while running: %+v", v)
	}
	close(release)
	for deadline := time.Now().Add(2 * time.Second); ; {
		v := r.List()[1]
		if v.Job.Done {
			if v.Job.Error != "network gone" || len(v.Job.Log) != 1 {
				t.Fatalf("finished job: %+v", v.Job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGroupsJSON(t *testing.T) {
	b, _ := json.Marshal(Groups[0])
	if string(b) != `{"id":"agents","title":"Agents","about":"The programs voice talks for and takes dictation to"}` {
		t.Fatalf("%s", b)
	}
}

func TestRequires(t *testing.T) {
	pyOn := false
	var order []string
	r := &Registry{}
	r.Add(Integration{ID: "python", Group: "gpu", Title: "Python",
		Status: func() Status {
			if pyOn {
				return Status{State: On}
			}
			return Status{State: Missing, Actions: []Action{{ID: "install", Long: true, Primary: true}}}
		},
		Do: func(_ context.Context, _ string, log func(string)) error {
			order = append(order, "python")
			pyOn = true
			return nil
		}})
	r.Add(Integration{ID: "xtts", Group: "gpu", Title: "XTTS", Requires: []string{"python"},
		Status: func() Status {
			return Status{State: Missing, Actions: []Action{{ID: "install", Long: true, Primary: true}}}
		},
		Do: func(_ context.Context, _ string, log func(string)) error { order = append(order, "xtts"); return nil }})

	if v := r.List()[1]; v.Status.Detail != "Installs Python first" {
		t.Fatalf("detail %q", v.Status.Detail)
	}
	if _, err := r.Start("xtts", "install"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); len(order) < 2 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Join(order, ",") != "python,xtts" {
		t.Fatalf("order %v", order)
	}
}
