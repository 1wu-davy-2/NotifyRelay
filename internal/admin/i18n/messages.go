package i18n

// Messages is the operator interface's copy for one language.
//
// Every field must be set in every table. The compiler catches a field that is
// missing from one of them; a test walks both and fails on one that is present
// but blank, because a blank string compiles perfectly and renders as nothing
// at all.
//
// A field that interpolates a value carries a %s or %d, and its comment says
// what goes in.
type Messages struct {
	// Script is the subset app.js reads, rendered into the page as JSON.
	//
	// A separate type rather than a method returning a map, so the JSON block
	// cannot drift into being a second, untyped copy of the table: a string the
	// script needs has to be declared here, and the compiler says so when one is
	// added to the script and not to the table.
	Script Script

	// ---------------------------------------------------------------- shell

	Brand      string
	SignOut    string
	LangSwitch string // accessible label for the language switcher

	NavChannels   string
	NavDeliveries string
	NavAudit      string
	NavKeys       string
	NavAPI        string

	// Page titles. Separate from the navigation labels because a nav item and a
	// page heading are not always the same words in either language — the nav
	// has to be short and the title does not.
	TitleChannels   string
	TitleDeliveries string
	TitleAudit      string
	TitleKeys       string
	TitleAPI        string

	// --------------------------------------------------------- relative time

	TimeJustNow    string
	TimeMinutesAgo string // %d minutes
	TimeHoursAgo   string // %d hours
	TimeDaysAgo    string // %d days

	// --------------------------------------------------------------- common

	CommonSave   string
	CommonCancel string
	CommonDelete string
	CommonCreate string
	CommonCopy   string
	CommonDone   string
	CommonNever  string
	CommonTest   string

	// ---------------------------------------------------------------- login

	TitleLogin    string
	LoginIntro    string
	LoginUsername string
	LoginPassword string
	LoginSubmit   string

	// ---------------------------------------------------------------- setup

	TitleSetup       string
	SetupIntro       string
	SetupUsername    string
	SetupPassword    string
	SetupConfirm     string
	SetupSubmit      string
	SetupPasswordWhy string
	SetupMismatch    string

	// ---------------------------------------------------------------- errors

	TitleError string

	// The error page's way out. One per page rather than a "Back to %s", because
	// the label names a place and the two languages name it differently: a
	// title that works as a heading does not always work inside a sentence.
	BackChannels   string
	BackDeliveries string
	BackAudit      string
	BackKeys       string
	BackAPI        string
	BackSetup      string

	ErrAdminUnreadable string
}

// Script holds the strings app.js shows: the confirms, the alerts and the one
// prompt. They are here rather than in the templates because the script builds
// them at the moment it needs them, and because three of the four confirmations
// interpolate a name the server never sees.
type Script struct {
	// The confirmations. Each takes one value, interpolated by the script.
	ConfirmResetBreaker string // %s: the channel's name
	ConfirmDeleteChan   string // %s: the channel's name
	ConfirmDeleteKey    string // %s: the API key's name
	ConfirmReplay       string // %s: the delivery id

	SavedButNotLive string // %s: why the running service refused it
	SaveBeforeTest  string
	ChooseTypeFirst string
	GiveKeyAName    string
	PasswordsDiffer string

	PromptCopyToken string // %s: the token
	PromptCopyText  string // %s: the text that could not be copied

	// TestResult reports a connectivity check. %s is the class, %s the detail.
	//
	// Two forms today. The third — "the configuration was checked and nothing
	// was called", which is the honest answer for the five channel types with no
	// side-effect-free probe — needs channel.Result to say whether the peer was
	// contacted, and that is a change to the most load-bearing type in the
	// codebase. It is planned, and this is where its copy goes when it lands.
	TestOK     string // %s: what was verified
	TestFailed string // %s: class, %s: the error
}
