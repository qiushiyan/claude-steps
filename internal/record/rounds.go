package record

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// This file joins what a session's Bash calls show of envoy into round
// events. It alone decides which round a collect read, which dispatch
// replaced which, and what a call's output proves: the decoder hands it each
// call's commands in the order they ran and then the call's output, and
// nothing else writes a round's fields.

var (
	// envoy heads a job's block with "job: <dir>" and a fan-out's with
	// "fan-out: <dir>"; "status: ok" or "status: partial — 1 of 2 …" follows.
	// A collect prints the status on the next line. A run prints its
	// settings there and the status when the job ends. Inside a fan-out,
	// "=== member <name> ===" opens each member's own block.
	envoyLine = regexp.MustCompile(`^(?:job:[ \t]*(\S+)|fan-out:[ \t]*(\S*/\S*)|status:[ \t]*([A-Za-z][\w-]*)|(=== member .* ===))`)
	// The two lines on their own, for the second trace and for a status no
	// block holds.
	statusLine = regexp.MustCompile(`(?m)^status:[ \t]*([A-Za-z][\w-]*)`)
	jobLine    = regexp.MustCompile(`(?m)^job:[ \t]*(\S+)`)
	jobReuse   = regexp.MustCompile(`\+\d+$`)
	envoyCall  = regexp.MustCompile(`\benvoy\s+(run|collect)\b`)
)

// roundCall is what one Bash call ran of envoy.
type roundCall struct {
	ops      []roundOp // its runs, and its collects of a job the text names, in command order
	unnamed  int       // collects whose job the text does not give
	probes   int       // `envoy collect --status-only` calls
	mentions bool      // the text holds "envoy run" or "envoy collect" somewhere
}

// roundOp is one envoy run, or one collect of a job the text names.
type roundOp struct {
	job string
	run bool
	// round is the event a run added. For a collect it is the round the
	// name read when the command ran, or -1 when the name had none.
	round int
	// For a run: the round it replaced, and the round its name read before
	// it. Each is -1 when there was none.
	replaced, before int
}

type rounds struct {
	rec     *Record
	sig     *Signal        // the round fact's two counts
	latest  map[string]int // job name → the round a collect by that name reads
	awaited map[int]bool   // rounds a collect call has named and has not returned for
	prompt  *int           // the human prompts read so far
}

func newRounds(rec *Record, sig *Signal, prompt *int) rounds {
	return rounds{rec: rec, sig: sig, prompt: prompt, latest: map[string]int{}, awaited: map[int]bool{}}
}

// command records one simple command of a Bash call when it is an envoy run
// or collect. A run's event is added now, at the dispatch. A collect is
// joined to the round its name reads now, so a dispatch later in the same
// call does not take it.
func (r *rounds) command(call *roundCall, at time.Time, argv []string) {
	if job, ok := envoyJob(argv, "run"); ok {
		if !named(job) {
			// A dispatch under a name the text does not give is a round the
			// reader cannot show.
			r.sig.Second++
			r.sig.Missed++
			return
		}
		call.ops = append(call.ops, r.dispatch(at, job))
		r.sig.Primary++
	}
	if arg, ok := envoyJob(argv, "collect"); ok {
		switch {
		// `collect --status-only` asks whether the job is still running; it
		// delivers no result, so it is not a collect.
		case slices.Contains(argv, "--status-only"):
			call.probes++
		case !named(arg):
			call.unnamed++
		default:
			op := roundOp{job: r.jobName(arg), round: -1}
			if idx, ok := r.latest[op.job]; ok {
				op.round = idx
				r.awaited[idx] = true
			}
			call.ops = append(call.ops, op)
		}
		r.sig.Primary++
	}
}

// dispatch adds a round for a run under job. The round the name read until
// now is replaced when it is still waiting for a collect: envoy collects a
// name as its latest dispatch, so no collect of the earlier one can follow.
// The new round stands for the dispatches the earlier one stood for.
func (r *rounds) dispatch(at time.Time, job string) roundOp {
	op := roundOp{job: job, run: true, replaced: -1, before: -1}
	e := Event{At: at, Kind: Round, Name: job, Dispatched: true, Prompt: *r.prompt}
	if prev, ok := r.latest[job]; ok {
		op.before = prev
		if p := &r.rec.Events[prev]; p.Waiting() && !r.awaited[prev] {
			p.Redispatched = true
			op.replaced = prev
			e.Dispatches = max(p.Dispatches, 1) + 1
		}
	}
	r.rec.Events = append(r.rec.Events, e)
	op.round = len(r.rec.Events) - 1
	r.latest[job] = op.round
	return op
}

// settle reads a call's result: whether each run dispatched anything, and
// what envoy said of each job collected. Each command takes its own block of
// the output, a run's or a collect's, in the order the commands ran.
func (r *rounds) settle(call *roundCall, called, returned time.Time, failed bool, text string) {
	blocks := envoyBlocks(text)
	take := func(job string, collect bool) (status string, printed bool) {
		for i := range blocks {
			if b := &blocks[i]; !b.taken && b.collect == collect && r.jobName(b.dir) == job {
				b.taken = true
				return b.status, true
			}
		}
		return "", false
	}
	for _, op := range call.ops {
		status, printed := take(op.job, !op.run)
		if op.run {
			r.ran(op, failed, status, printed)
			continue
		}
		// A collect sent through a filter or to a file may print no block of
		// its own. The call's first status line is then its status, when it
		// is the only envoy command the call ran.
		if !printed && len(call.ops) == 1 && call.unnamed == 0 {
			if m := statusLine.FindStringSubmatch(text); m != nil {
				status = m[1]
			}
		}
		r.collected(op.job, op.round, called, returned, status, failed && status == "")
	}
	// A collect whose job the text does not give (a loop's variable, a
	// command substitution) is named by the collect blocks no other command
	// took. With none, it is a round the reader cannot show.
	if call.unnamed > 0 {
		found := false
		for i := range blocks {
			b := &blocks[i]
			if b.taken || !b.collect {
				continue
			}
			b.taken, found = true, true
			job, round := r.jobName(b.dir), -1
			if idx, ok := r.latest[job]; ok {
				round = idx
			}
			r.collected(job, round, called, returned, b.status, failed && b.status == "")
		}
		if !found {
			r.sig.Second++
			r.sig.Missed++
		}
	}
	if jobLine.MatchString(text) && statusLine.MatchString(text) {
		r.sig.Second++
		if len(call.ops)+call.unnamed+call.probes == 0 && call.mentions {
			r.sig.Missed++
		}
	}
}

// ran settles a run with its call's result. A call that returned an error
// failed the run, unless envoy's own block says the job ended ok: then a
// later command of the call failed. A failed run that printed its job line
// did create the job, and a collect by the name reads it. One that printed
// none ran nothing: it replaced no round, and the name reads what it read
// before.
func (r *rounds) ran(op roundOp, failed bool, status string, printed bool) {
	if !failed || status == "ok" {
		return
	}
	e := &r.rec.Events[op.round]
	e.Failed = true
	if printed {
		return
	}
	e.Dispatches = 0
	if op.replaced >= 0 {
		r.rec.Events[op.replaced].Redispatched = false
	}
	if r.latest[op.job] == op.round {
		if op.before >= 0 {
			r.latest[op.job] = op.before
		} else {
			delete(r.latest, op.job)
		}
	}
}

// collected joins a collect to the round it read. A later collect that
// printed a status replaces an earlier one; one that printed none
// (`--result-only`, or output sent to a file) leaves the status already
// read. A job the name read no round for becomes a round of its own, dated
// at the collect call.
func (r *rounds) collected(job string, round int, called, returned time.Time, status string, failed bool) {
	if round < 0 {
		r.rec.Events = append(r.rec.Events, Event{At: called, Kind: Round, Name: job, Prompt: *r.prompt})
		round = len(r.rec.Events) - 1
		if _, ok := r.latest[job]; !ok {
			r.latest[job] = round
		}
	}
	delete(r.awaited, round)
	e := &r.rec.Events[round]
	if e.CollectedAt != nil && status == "" && !failed {
		return
	}
	e.CollectedAt, e.Outcome, e.CollectFailed = &returned, status, failed
}

// jobName reduces a collect's argument, or a directory envoy printed, to a
// job name. envoy takes a name or a directory path; a path ends in the job's
// directory, which carries "+2", "+3" when the name was reused, or in a
// fan-out member's directory under it.
func (r *rounds) jobName(arg string) string {
	if !strings.Contains(arg, "/") {
		return arg
	}
	clean := path.Clean(arg)
	base := jobReuse.ReplaceAllString(path.Base(clean), "")
	parent := jobReuse.ReplaceAllString(path.Base(path.Dir(clean)), "")
	if _, ok := r.latest[base]; !ok {
		if _, ok := r.latest[parent]; ok {
			return parent
		}
	}
	return base
}

// envoyBlock is one job's block in a call's output: the directory it names
// and the first word of its status line, "" when it printed none.
type envoyBlock struct {
	dir, status string
	collect     bool // a collect printed it: the status is on the line after the job's
	taken       bool
}

// envoyBlocks reads the blocks a call's output holds, in order. A fan-out's
// own block says the round's status, so its members' blocks are left out.
func envoyBlocks(text string) []envoyBlock {
	var out []envoyBlock
	open, member, headed := false, false, false
	for line := range strings.Lines(text) {
		m := envoyLine.FindStringSubmatch(line)
		after := headed // the line before this one headed a block
		headed = false
		if m == nil {
			continue
		}
		dir := m[1] + m[2]
		switch {
		case m[4] != "":
			open, member = false, true
		case member:
			// The line a member's section opens with, its job or its status
			// alone, is the member's.
			member = false
		case dir != "":
			out = append(out, envoyBlock{dir: dir})
			open, headed = true, true
		case open && out[len(out)-1].status == "":
			out[len(out)-1].status, out[len(out)-1].collect = m[3], after
		}
	}
	return out
}
