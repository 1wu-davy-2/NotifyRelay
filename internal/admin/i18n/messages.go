package i18n

import "html/template"

// Messages is the operator interface's copy for one language.
//
// Every field must be set in every table. The compiler catches a field that is
// missing from one of them; a test walks both and fails on one that is present
// but blank, because a blank string compiles perfectly and renders as nothing
// at all — a button with no label, a heading that is not there.
//
// A field that interpolates a value carries a %s or %d, and its comment says
// what goes in. A field typed template.HTML carries markup and is rendered
// unescaped; see the note above those fields.
type Messages struct {
	// Script is the subset app.js reads, rendered into the page as JSON.
	//
	// A separate type rather than a method returning a map, so the JSON block
	// cannot drift into being a second, untyped copy of the table: a string the
	// script needs has to be declared here, and the compiler says so when one is
	// added to the script and not to the table.
	Script Script

	// ---------------------------------------------------------------- brand

	// AppName is the product's name in this language. It is also the suffix of
	// every page title, which is why it is here and not in the templates.
	AppName string

	// ----------------------------------------------------------- navigation

	NavChannels   string
	NavDeliveries string
	NavAudit      string
	NavKeys       string
	NavAPI        string
	NavSignOut    string
	NavLangSwitch string // accessible label for the language switcher
	NavStart      string // the first-run checklist, offered only while it is unfinished

	// The theme switch, which only the client-side interface has. The
	// server-rendered pages are dark and nothing else, so these are the first
	// strings in the table that nothing in the templates reads — they are here
	// rather than in the frontend for the reason the whole table is: a sentence
	// belongs in one place, and this is it.
	NavThemeSwitch string // accessible label for the switch
	ThemeDark      string // the theme's name while it is dark
	ThemeLight     string // the theme's name while it is light

	// ---------------------------------------------------------- page titles

	// Separate from the navigation labels because a nav item and a page heading
	// are not always the same words in either language — the nav has to be
	// short and the title does not. Also used as the h1 where the two agree.
	TitleSignIn     string
	TitleSetup      string
	TitleChannels   string
	TitleDeliveries string
	TitleDelivery   string
	TitleAudit      string
	TitleKeys       string
	TitleAPI        string
	TitleError      string // the error page, whose heading is the error and not the page
	TitleStart      string

	// ------------------------------------------------ the first-run checklist

	StartIntro           string
	StartStepChannel     string
	StartStepChannelHint string
	StartStepKey         string
	StartStepKeyHint     string
	StartStepTest        string
	StartStepTestHint    string
	StartStepResult      string
	StartStepResultHint  string
	StartActionGo        string
	StartStepDone        string
	StartDoneHead        string
	StartDoneBody        string

	// ---------------------------------------------------------- shared copy

	CommonSave        string
	CommonCancel      string
	CommonDelete      string
	CommonTest        string
	CommonCopy        string
	CommonDone        string
	CommonCreate      string
	CommonEnable      string
	CommonDisable     string
	CommonFilter      string
	CommonClear       string
	CommonDetail      string
	CommonReplay      string
	CommonBack        string
	CommonNewer       string
	CommonOlder       string
	CommonAny         string
	CommonNever       string
	CommonEnabled     string
	CommonDisabled    string
	CommonRequired    string // the required marker's title attribute
	CommonSetInConfig string // the key list's source column

	// --------------------------------------------------------------- login

	LoginSubtitle string
	LoginUsername string
	LoginPassword string
	LoginSubmit   string

	// --------------------------------------------------------------- setup

	SetupIntro        string
	SetupUsername     string
	SetupPassword     string
	SetupConfirm      string
	SetupSubmit       string
	SetupPasswordRule string

	// ------------------------------------------------------- change password

	NavPassword     string
	TitlePassword   string
	PasswordIntro   string
	PasswordCurrent string
	PasswordNew     string
	PasswordConfirm string
	PasswordSubmit  string
	PasswordRule    string
	PasswordChanged string // %d: how many other sessions were signed out

	// ------------------------------------------- the error page's way out

	// One per page rather than a "Back to %s", because the label names a place
	// and the two languages name it differently: a title that works as a
	// heading does not always work inside a sentence.
	BackToChannels   string
	BackToDeliveries string
	BackToAudit      string
	BackToKeys       string
	BackToAPI        string
	BackToSetup      string

	// ------------------------------------------------------------ channels

	ChannelsNew            string // the header button, and the empty state's action
	ChannelsTableName      string
	ChannelsTableType      string
	ChannelsTableState     string
	ChannelsTableSecrets   string
	ChannelsTagDisabled    string
	ChannelsTagLive        string
	ChannelsTagNotLoaded   string
	ChannelsResetBreaker   string
	ChannelsEmptyTitle     string
	ChannelsEmptyBody      string
	ChannelsEmptyAction    string
	ChannelFormEditHeading string // %s: the channel's name
	ChannelFormNameLabel   string
	ChannelFormNameDesc    string
	ChannelFormTypeLabel   string
	ChannelFormTypeHint    string // the type picker's placeholder entry
	ChannelFormTypeDesc    string
	ChannelFormEnabled     string
	ChannelFormConfigHead  string
	ChannelFormSecretHint  string // a private parameter's placeholder
	ChannelFormSecretClear string
	ChannelFormQuotaHead   string
	ChannelFormQuotaDesc   string
	ChannelFormTestConn    string
	// ChannelFormListHint is shown under a parameter the schema declares as a
	// list. The form splits on commas and the schema's own description does not
	// say so, which leaves the convention undocumented on the page.
	ChannelFormListHint string

	// The edit form for a channel that is not there.
	ChannelGoneHead string // %s: the name that was asked for
	ChannelGoneBody string // %s: the name that was asked for

	// Sending a real notification through a channel, which is the only thing on
	// this page that proves a notification arrives.
	TestNotifyButton  string
	TestNotifyHeading string
	TestNotifyIntro   string
	// TestNotifyNeedsRecipient is the tooltip on a disabled test button.
	TestNotifyNeedsRecipient string
	TestNotifyTitle          string
	TestNotifyTitleHint      string
	TestNotifyBody           string
	TestNotifyBodyHint       string
	TestNotifySubmit         string
	TestNotifyCancel         string

	// ---------------------------------------------------------- deliveries

	DeliveriesStats           string // %d queued, %d sending, %d sent, %d failed
	DeliveriesFilterStatus    string
	DeliveriesFilterChannel   string
	DeliveriesFilterRequestID string
	DeliveriesFilterLimit     string
	DeliveriesTableCreated    string
	DeliveriesTableChannel    string
	DeliveriesTableStatus     string
	DeliveriesTableClass      string
	DeliveriesTableAttempts   string
	DeliveriesTableLastError  string
	DeliveriesEmptyTitle      string // nothing has ever been sent
	DeliveriesEmptyBody       string
	DeliveriesEmptyAction     string
	DeliveriesNoMatchTitle    string // there is a filter and it matched nothing
	DeliveriesNoMatchBody     string
	DeliveriesNoMatchAction   string

	// The four status labels.
	//
	// Only the label. The value in the query string and the filter's option
	// values stay as the English enum the store uses — translating those would
	// change what the API accepts, which is a different change from translating
	// what the page says.
	StatusQueued  string
	StatusSending string
	StatusSent    string
	StatusFailed  string

	// ------------------------------------------------------- delivery detail

	DeliveryFactID          string
	DeliveryFactRequest     string
	DeliveryFactChannel     string
	DeliveryFactStatus      string
	DeliveryFactAttempts    string
	DeliveryFactCreated     string
	DeliveryFactSent        string
	DeliveryFactNextAttempt string
	DeliveryFactLastError   string
	DeliveryBodyExpired     string // the body is gone, so it cannot be replayed
	DeliveryNotDeadLetter   string
	DeliveryAttemptsHeading string
	DeliveryAttemptWhen     string
	DeliveryAttemptClass    string
	DeliveryAttemptSkipped  string
	DeliveryAttemptDetail   string
	DeliveryAttemptError    string
	DeliveryEmptyTitle      string // no attempts recorded
	DeliveryEmptyBody       string // names the three reasons a delivery is never attempted

	// --------------------------------------------------------------- audit

	AuditIntro        string
	AuditTableWhen    string
	AuditTableWho     string
	AuditTableAction  string
	AuditTableChannel string
	AuditTableDetail  string
	AuditDisabledHead string // this deployment keeps no audit trail
	AuditDisabledBody string
	AuditEmptyHead    string // it does, and nothing has happened yet
	AuditEmptyBody    string

	// ---------------------------------------------------------------- keys

	KeysIntro            string
	KeysNewHeading       string
	KeysNameLabel        string
	KeysNameHint         string // the input's placeholder example
	KeysCreateButton     string
	KeysNameDesc         string
	KeysConfiguredNotice string // %d keys come from the configuration file
	KeysTableName        string
	KeysTableSource      string
	KeysTableStatus      string
	KeysTableCreated     string
	KeysTableLastUsed    string
	KeysTableRecipients  string
	// KeysRecipientsLabel, KeysRecipientsHint and KeysRecipientsDesc describe
	// the address allow list: the one field on this page that decides what a
	// key can reach beyond the channels an operator already configured.
	KeysRecipientsLabel   string
	KeysRecipientsHint    string // the input's placeholder example
	KeysRecipientsDesc    string
	KeysRecipientsNone    string // the table cell for a key that may address nobody
	KeysRecipientsEdit    string // the row button
	KeysRecipientsHeading string // the edit dialog's heading, then the key's name
	KeysEmptyTitle        string
	KeysEmptyBody         template.HTML // names the status code, so it carries <code>
	KeysDialogHeading     string
	KeysDialogBody        string
	KeysDialogCopy        string
	KeysDialogDone        string

	// ------------------------------------------------------------ API docs

	// The nine template.HTML fields in this struct carry markup — <code>,
	// <strong>, <em> — and are rendered unescaped.
	//
	// That is safe here for one reason and it is worth stating: every one of
	// them is a constant written in this package, and no part of any of them
	// comes from a request, a configuration file or a database. A translation
	// is code, reviewed like code. If that ever stops being true, these become
	// an injection point, and the test that restricts them to a small tag
	// allowlist is what will notice.
	APIDocsIntro           template.HTML
	APIDocsBaseURL         string
	APIDocsAuth            string
	APIDocsBaseURLNote     string
	APIDocsTokenNote       template.HTML
	APIDocsPlainHTTP       template.HTML
	APIDocsExamplesHeading string
	APIDocsExamplesIntro   template.HTML
	APIDocsCopy            string
	// The recipients section: the one part of a request the samples do not
	// show, because they all send to a channel's own destination. The four
	// body strings carry <code> and <strong>, like the API copy above them.
	APIDocsRecipientsHeading   string
	APIDocsRecipientsIntro     template.HTML
	APIDocsRecipientsSame      template.HTML
	APIDocsRecipientsReplace   template.HTML
	APIDocsRecipientsAllowlist template.HTML
	APIDocsRecipientsExclusive template.HTML
	// The two example bodies. Escaped on the way out and read inside a <pre>,
	// so they are plain strings rather than markup.
	APIDocsRecipientsExampleTo  string
	APIDocsRecipientsExampleURL string
	APIDocsEndpointsHead        string
	APIDocsTableMethod          string
	APIDocsTablePath            string
	APIDocsTableAuth            string
	APIDocsTablePurpose         string
	APIDocsAuthNone             string
	APIDocsErrorsHeading        string
	APIDocsErrorBodyNote        template.HTML
	APIDocsTableStatus          string
	APIDocsTableMeaning         string
	APIDocsGotchasHeading       string
	APIDocsGotchaClassCase      template.HTML
	APIDocsGotchaNotAtt         template.HTML
	APIDocsSampleNoteCurl       string
	APIDocsSampleNoteGo         string

	// ------------------------------------------------------- relative time

	TimeJustNow    string
	TimeMinutesAgo string // %d minutes
	TimeHoursAgo   string // %d hours
	TimeDaysAgo    string // %d days

	// ---------------------------------------------------------- error copy

	ErrChannelsUnreadable    string
	ErrChannelUnreadable     string
	ErrChannelNotFound       string
	ErrDeliveriesUnreadable  string
	ErrNoDeliveryStore       string
	ErrDeliveryUnreadable    string
	ErrDeliveryNotFound      string
	ErrAttemptsUnreadable    string
	ErrAuditUnreadable       string
	ErrInvalidJSON           string
	ErrChannelNameRequired   string
	ErrChannelNameTaken      string // %s: the channel's name
	ErrChannelDeleteFailed   string
	ErrBreakerDisabled       string
	ErrChannelBuildFailed    string
	ErrChannelDisabled       string
	ErrChannelNeedsRecipient string
	ErrKeyRecipientPattern   string // %s: the refused pattern
	ErrNoQueue               string
	ErrEnqueueFailed         string
	ErrTestMessageIncomplete string
	ErrInvalidIntParam       string // %s: the parameter's name (limit, offset)
	ErrNotReplayableStatus   string // %s: the delivery's current status
	ErrBodyExpired           string
	ErrReplayFailed          string
	ErrNotReplayable         string
	ErrStatsUnreadable       string
	ErrKeysUnreadable        string
	ErrNoKeyStore            string
	ErrKeyNameRequired       string
	ErrKeyCreateFailed       string
	ErrKeyUnreadable         string
	ErrKeyNotFound           string
	ErrKeyUpdateFailed       string
	ErrKeyDeleteFailed       string
	ErrTokenOnce             string
	ErrAdminUnreadable       string
	ErrTooManyAttempts       string
	ErrUsernameRequired      string
	ErrPasswordTooShort      string // %d: the minimum length
	ErrNoCredentialStore     string
	ErrAccountCreateFailed   string
	ErrAlreadyConfigured     string
	ErrSessionCreateFailed   string
	ErrTooManySignIns        string
	ErrBadCredentials        string
	ErrSignInFirst           string
	ErrSessionExpired        string
	ErrPasswordInConfig      string
	ErrPasswordChangeFailed  string
	ErrMissingCSRF           string // %s: the header's name

	// ErrNotFound answers a path under /api that no route matched.
	//
	// The interface reaches it by asking for something that is not there, which
	// is a bug in the interface rather than in the request — and it renders
	// whatever comes back, so an English sentence would surface to a Chinese
	// operator. Translated for that reason and not because a stranger reads it.
	ErrNotFound string
}

// Script holds the strings app.js shows: the confirms, the alerts, the one
// prompt, and the flash messages it builds after a redirect.
//
// They are here rather than in the templates because the script assembles them
// at the moment it needs them, and because four of the confirmations
// interpolate a name the server never sees.
type Script struct {
	// The confirmations. Each takes one value, interpolated by the script.
	ConfirmResetBreaker string // %s: the channel's name
	ConfirmDeleteChan   string // %s: the channel's name
	ConfirmDeleteKey    string // %s: the API key's name
	ConfirmReplay       string // %s: the delivery id

	// ConfirmReplace is the second half of the name-collision confirmation: the
	// server refuses, the script asks, and this is what it asks with.
	ConfirmReplace string

	// Flash messages, built after the action succeeded and the page reloaded.
	SavedFlash        string // %s: the channel's name
	DeletedFlash      string // %s: what was deleted
	ReplayedFlash     string // %s: the delivery id
	BreakerResetFlash string // %s: the channel's name, %s: the state it was in

	SavedButNotLive  string // %s: why the running service refused it
	ChooseTypeFirst  string
	SaveBeforeTest   string
	KeyNameRequired  string
	PasswordsNoMatch string
	SetupFailed      string // the first-run form's fallback error

	CopyTokenPrompt  string
	CopyManualPrompt string

	// TestResult reports a connectivity check. %s is the class, %s the detail.
	//
	// Two forms today. The third — "the configuration was checked and nothing
	// was called", which is the honest answer for the five channel types with no
	// side-effect-free probe — needs channel.Result to say whether the peer was
	// contacted, and that is a change to the most load-bearing type in the
	// codebase. It is planned, and this is where its copy goes when it lands.
	TestOK     string // %s: what was verified
	TestFailed string // %s: class, %s: the error

	// TestNotifyQueued is the fallback when the queue took the message but the
	// response carried no delivery id to go and watch.
	TestNotifyQueued string

	// KeyStateEnabled and KeyStateDisabled are the flash after toggling a key.
	// The action changes what can authenticate against the whole service, and it
	// used to reload the page with nothing said about it.
	KeyStateEnabled  string // %s: the key's name
	KeyStateDisabled string // %s: the key's name
	// KeyRecipientsSaved is the flash after a key's address allow list is
	// saved. %s: the key's name.
	KeyRecipientsSaved string

	// DiscardChanges guards a form that has been edited and not saved.
	DiscardChanges string

	// PasswordSet is the fallback for a password change whose response carried
	// no message. The server always sends one — it is the only side that knows
	// how many sessions ended — so this is for the case where it did not.
	PasswordSet string

	// FixMarkedFields replaces the summary when every complaint found a field to
	// sit under. Repeating them at the bottom would be the same sentences twice.
	FixMarkedFields string
}
