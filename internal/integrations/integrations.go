// Package integrations is the list behind voice's Integrations window: the
// agents voice is wired into, its speech, cloning and listening models, the
// GPU backends. Each integration is a module of the same shape, a status and
// the actions it offers, so a new one is one more entry in the registry and
// the window needs no change. Actions that take long (a download, a backend
// setup) run as jobs whose log the window follows.
package integrations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// State is how an integration stands.
type State string

const (
	On      State = "on"      // installed and in use
	Off     State = "off"     // installed, switched off
	Missing State = "missing" // not installed; an action adds it
	Partly  State = "partly"  // some of it is set up, or it needs a step by the user
	Error   State = "error"   // it should work and does not
	Planned State = "planned" // not in voice yet: listed so it can be seen, nothing to install
)

// Action is something the user can do with an integration.
type Action struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Long actions run as a job with a log (a download, a setup).
	Long bool `json:"long,omitempty"`
	// Primary is the one the window shows as the main button.
	Primary bool `json:"primary,omitempty"`
	// Disabled actions show greyed out, with Why (this PC lacks the
	// graphics card it needs), and do not run.
	Disabled bool   `json:"disabled,omitempty"`
	Why      string `json:"why,omitempty"`
}

// Status is an integration's state now, with a line saying why, and what can
// be done.
type Status struct {
	State  State  `json:"state"`
	Detail string `json:"detail,omitempty"`
	// Label names the state when its usual name would mislead ("Starting"
	// for a server on its way up rather than "Needs a step").
	Label   string   `json:"label,omitempty"`
	Actions []Action `json:"actions,omitempty"`
	// Choice is a setting the window shows as a picker, such as a model
	// size; picking one runs the action "choose:<value>".
	Choice *Choice `json:"choice,omitempty"`
	// Field is a text setting the window shows as an input, such as a
	// server's address; saving it runs the action "set:<value>".
	Field *Field `json:"field,omitempty"`
}

// Field is one text setting.
type Field struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Placeholder string `json:"placeholder,omitempty"`
}

// SetPrefix starts the action that saves a Field value.
const SetPrefix = "set:"

// Choice is one setting with its values.
type Choice struct {
	Label   string         `json:"label"`
	Value   string         `json:"value"` // the one in use
	Options []ChoiceOption `json:"options"`
}

type ChoiceOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
}

// ChoosePrefix starts the action that picks a Choice value.
const ChoosePrefix = "choose:"

// Integration is one module. Status must be quick (it runs on every refresh
// of the window); Do runs an action and writes what it does to log.
type Integration struct {
	ID    string
	Group string // one of Groups
	Title string
	About string // one sentence: what it gives the user
	Size  string // what it takes on disk, when it is a download
	Link  string // where to read more
	// Requires names integrations this one needs: a long action installs
	// the missing ones first (their primary action), in the same job.
	Requires []string
	// Also names other groups the integration shows in: a GPU voice that
	// clones is under Cloning too.
	Also []string
	// Languages it speaks or recognizes (en, pl, ...; "*" for nearly all);
	// empty when language does not apply. The window compares them with the
	// languages the user speaks.
	Languages []string
	// Expressive is what a voice engine does with voice's sound tags and
	// moods; nil when it is not a voice engine.
	Expressive *Expressive
	// RunsOn names the graphics cards it needs ("an NVIDIA graphics card"); empty is any.
	RunsOn string
	// Status reports the state; Do runs one of the actions Status offered.
	Status func() Status
	Do     func(ctx context.Context, action string, log func(string)) error
}

// Expressive is a voice engine's support of voice's tags ({laugh}) and moods,
// in one form for every engine: the sounds it makes, how it shows a mood
// (none, intensity, labels or instruct) and which moods.
type Expressive struct {
	Sounds  []string `json:"sounds"`
	Emotion string   `json:"emotion"`
	Moods   []string `json:"moods,omitempty"`
}

// Group is a section of the window.
type Group struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	About string `json:"about"`
}

// Groups orders the window's sections.
var Groups = []Group{
	{"agents", "Agents", "The programs voice talks for and takes dictation to"},
	{"speech", "Speaking", "Voices for what agents say"},
	{"gpu", "GPU voices", "Larger voices and cloning in more languages, on a graphics card"},
	{"cloning", "Cloning", "Engines that make a voice from a recording"},
	{"listening", "Listening", "Speech recognition for dictation and answers"},
}

// View is an integration as the window shows it.
type View struct {
	ID        string   `json:"id"`
	Group     string   `json:"group"`
	Also      []string `json:"also,omitempty"`
	Languages []string `json:"languages,omitempty"`
	// Expressive: a voice engine's sounds and moods (see Expressive).
	Expressive *Expressive `json:"expressive,omitempty"`
	RunsOn     string      `json:"runs_on,omitempty"`
	Title      string      `json:"title"`
	About      string      `json:"about,omitempty"`
	Size       string      `json:"size,omitempty"`
	Link       string      `json:"link,omitempty"`
	Status     Status      `json:"status"`
	Job        *Job        `json:"job,omitempty"` // the running or last action
}

// Job is an action that runs in the background.
type Job struct {
	ID      string    `json:"id"`
	Action  string    `json:"action"`
	Started time.Time `json:"started"`
	Done    bool      `json:"done"`
	Error   string    `json:"error,omitempty"`
	Log     []string  `json:"log"`
}

// Registry holds the integrations and their jobs.
type Registry struct {
	mu    sync.Mutex
	items []Integration
	jobs  map[string]*Job // by integration ID: the latest
	seq   int
}

// Add registers an integration; a later one with the same ID replaces it.
func (r *Registry) Add(i Integration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, x := range r.items {
		if x.ID == i.ID {
			r.items[k] = i
			return
		}
	}
	r.items = append(r.items, i)
}

// List is every integration with its status, in group order.
func (r *Registry) List() []View {
	r.mu.Lock()
	items := append([]Integration(nil), r.items...)
	jobs := map[string]Job{}
	for id, j := range r.jobs {
		jobs[id] = j.copy()
	}
	r.mu.Unlock()
	order := map[string]int{}
	for k, g := range Groups {
		order[g.ID] = k
	}
	sort.SliceStable(items, func(a, b int) bool { return order[items[a].Group] < order[items[b].Group] })
	out := make([]View, 0, len(items))
	for _, i := range items {
		v := View{ID: i.ID, Group: i.Group, Also: i.Also, Title: i.Title, About: i.About, Size: i.Size, Link: i.Link, Languages: i.Languages, Expressive: i.Expressive, RunsOn: i.RunsOn}
		v.Status = safeStatus(i)
		if missing := r.missing(i, items); len(missing) > 0 && v.Status.State != On {
			names := make([]string, len(missing))
			for k, m := range missing {
				names[k] = m.Title
			}
			v.Status.Detail = strings.TrimSpace(v.Status.Detail + " · Installs " + strings.Join(names, ", ") + " first")
			v.Status.Detail = strings.TrimPrefix(v.Status.Detail, "· ")
		}
		if j, ok := jobs[i.ID]; ok {
			v.Job = &j
			if !j.Done {
				v.Status.Actions = nil // one action at a time
			}
		}
		out = append(out, v)
	}
	return out
}

func safeStatus(i Integration) (st Status) {
	defer func() {
		if p := recover(); p != nil {
			st = Status{State: Error, Detail: fmt.Sprint("status failed: ", p)}
		}
	}()
	if i.Status == nil {
		return Status{State: On}
	}
	return i.Status()
}

// Start runs action on integration id: at once for a short action, as a job
// for a long one. It returns the job; a short action's job is already done.
func (r *Registry) Start(id, action string) (Job, error) {
	r.mu.Lock()
	var it *Integration
	for k := range r.items {
		if r.items[k].ID == id {
			it = &r.items[k]
		}
	}
	if it == nil {
		r.mu.Unlock()
		return Job{}, fmt.Errorf("no integration %q", id)
	}
	if j := r.jobs[id]; j != nil && !j.Done {
		r.mu.Unlock()
		return Job{}, fmt.Errorf("%s is busy with %s", it.Title, j.Action)
	}
	i := *it
	r.mu.Unlock()

	// Picking a value is quick and needs nothing installed first: it runs now.
	if value, ok := strings.CutPrefix(action, ChoosePrefix); ok {
		st := safeStatus(i)
		known := false
		if st.Choice != nil {
			for _, o := range st.Choice.Options {
				known = known || o.Value == value
			}
		}
		if !known || i.Do == nil {
			return Job{}, fmt.Errorf("%s has no choice %q", i.Title, value)
		}
		j := Job{ID: id + "-choose", Action: action, Started: time.Now(), Done: true, Log: []string{}}
		if err := i.Do(context.Background(), action, func(string) {}); err != nil {
			j.Error = err.Error()
			return j, err
		}
		return j, nil
	}

	// So is saving a text setting.
	if _, ok := strings.CutPrefix(action, SetPrefix); ok {
		if safeStatus(i).Field == nil || i.Do == nil {
			return Job{}, fmt.Errorf("%s has no setting to save", i.Title)
		}
		j := Job{ID: id + "-set", Action: action, Started: time.Now(), Done: true, Log: []string{}}
		if err := i.Do(context.Background(), action, func(string) {}); err != nil {
			j.Error = err.Error()
			return j, err
		}
		return j, nil
	}

	var act *Action
	for _, a := range safeStatus(i).Actions {
		if a.ID == action {
			a := a
			act = &a
		}
	}
	if act == nil || i.Do == nil {
		return Job{}, fmt.Errorf("%s cannot %s now", i.Title, action)
	}
	if act.Disabled {
		return Job{}, fmt.Errorf("%s: %s", i.Title, act.Why)
	}

	r.mu.Lock()
	if r.jobs == nil {
		r.jobs = map[string]*Job{}
	}
	r.seq++
	j := &Job{ID: fmt.Sprintf("%s-%d", id, r.seq), Action: action, Started: time.Now(), Log: []string{}}
	r.jobs[id] = j
	r.mu.Unlock()

	log := func(line string) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return
		}
		r.mu.Lock()
		j.Log = append(j.Log, line)
		if n := len(j.Log); n > 400 {
			j.Log = append([]string(nil), j.Log[n-400:]...)
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	all := append([]Integration(nil), r.items...)
	r.mu.Unlock()
	needs := r.missing(i, all)
	run := func() {
		err := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("%v", p)
				}
			}()
			// What it requires goes first, by that integration's primary action.
			for _, dep := range needs {
				var first *Action
				for _, a := range safeStatus(dep).Actions {
					if a.Primary {
						a := a
						first = &a
						break
					}
				}
				if first == nil || dep.Do == nil {
					return fmt.Errorf("%s needs %s, which cannot be installed from here", i.Title, dep.Title)
				}
				log(dep.Title + " first:")
				if err := dep.Do(context.Background(), first.ID, log); err != nil {
					return fmt.Errorf("%s: %w", dep.Title, err)
				}
			}
			return i.Do(context.Background(), action, log)
		}()
		r.mu.Lock()
		j.Done = true
		if err != nil {
			j.Error = err.Error()
		}
		r.mu.Unlock()
	}
	if act.Long {
		go run()
	} else {
		run()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return j.copy(), nil
}

// missing are the integrations i requires that are not set up.
func (r *Registry) missing(i Integration, all []Integration) []Integration {
	var out []Integration
	for _, id := range i.Requires {
		for _, x := range all {
			if x.ID == id && safeStatus(x).State != On {
				out = append(out, x)
			}
		}
	}
	return out
}

func (j *Job) copy() Job {
	c := *j
	c.Log = append([]string(nil), j.Log...)
	return c
}
