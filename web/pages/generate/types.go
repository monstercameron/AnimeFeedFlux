package generatepage

// ListState is TODOS.md D-FLOW's per-screen state matrix: "every list and
// panel implements all six, or it is not done." Used for the rail
// (D2-05), the sampler's candidate list, and any other list this page
// owns.
type ListState int

const (
	ListLoading ListState = iota
	ListEmpty
	ListPopulated
	ListError
	ListDisabledWithReason
	ListDisconnected
)

func (s ListState) String() string {
	switch s {
	case ListLoading:
		return "loading"
	case ListEmpty:
		return "empty"
	case ListPopulated:
		return "populated"
	case ListError:
		return "error"
	case ListDisabledWithReason:
		return "disabled-with-reason"
	case ListDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// CandidateView is one of the sampler's four ways to look at a
// SampleCandidate (PLAN.md §12.3, TODOS.md D2-19..22). Order matches the
// plan's own enumeration ("1. rendered, 2. raw validated fields, 3. the
// exact feed XML, 4. the Slack card preview") and CandidateViews below
// preserves it so a tab strip built by ranging over CandidateViews shows
// them in the plan's stated order, ending on the one PLAN.md calls out as
// "the one that matters."
type CandidateView int

const (
	ViewRendered CandidateView = iota
	ViewRawFields
	ViewFeedXML
	ViewSlackCard
)

// CandidateViews is every view, in display order.
var CandidateViews = []CandidateView{ViewRendered, ViewRawFields, ViewFeedXML, ViewSlackCard}

// TranslationKey returns the generate.* i18n key for this view's tab
// label (TODOS.md D6-11: "all four candidate views... through i18n").
func (v CandidateView) TranslationKey() string {
	switch v {
	case ViewRendered:
		return "generate.sampler.view.rendered"
	case ViewRawFields:
		return "generate.sampler.view.rawFields"
	case ViewFeedXML:
		return "generate.sampler.view.feedXML"
	case ViewSlackCard:
		return "generate.sampler.view.slackCard"
	default:
		return "generate.sampler.view.unknown"
	}
}

// FieldErrors maps a server field path (FeedServiceValidateSpecResponse's
// FieldError.Field, e.g. "spec.cron" or "spec.sources[0].url") to its
// message, keyed by the *editor's* local field key (see MapFieldErrors) —
// PLAN.md §12.3: "Server-side validation errors render against the
// offending field", not in a banner.
type FieldErrors map[string]string

// For looks up the message for an editor field key. ok is false when the
// field has no error, which is the common case and is what lets a form
// field render nothing rather than an empty error slot.
func (fe FieldErrors) For(fieldKey string) (string, bool) {
	msg, ok := fe[fieldKey]
	return msg, ok
}

// HasAny reports whether any field currently has an error — used to
// disable Save without needing a second pass over the same map.
func (fe FieldErrors) HasAny() bool {
	return len(fe) > 0
}

// ConflictResolution is the operator's choice when an optimistic-
// concurrency conflict is detected on save (PLAN.md §11, TODOS.md D2-16:
// "a real merge choice — never a silent clobber").
type ConflictResolution int

const (
	// ResolveKeepMine discards the server's concurrent change and
	// resubmits the local draft against the new version.
	ResolveKeepMine ConflictResolution = iota
	// ResolveTakeTheirs discards the local draft and reloads the server's
	// version.
	ResolveTakeTheirs
	// ResolvePerField lets the operator pick, field by field, which side
	// wins — see ApplyResolution's perFieldKeepMine parameter.
	ResolvePerField
)

// FieldDiff is one field that differs between the operator's in-progress
// draft and the server's current (conflicting) version, keyed the same
// way as FieldErrors so the same editor field components can render both.
type FieldDiff struct {
	FieldKey string
	Mine     string
	Theirs   string
}
