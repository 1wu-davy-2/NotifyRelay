/**
 * The operator interface's copy, as the admin API serves it.
 *
 * GENERATED from internal/admin/i18n/messages.go by web/scripts/gen-messages.py.
 * Do not edit by hand: the next run overwrites it. Edit the Go table and
 * regenerate, which is also where the two translations live.
 *
 * Why mirror it at all, rather than fetch it loosely typed. A field the Go
 * table has and this does not is a TypeScript error at the line that uses it —
 * loud, immediate, and fixed by rerunning the generator. The alternative, an
 * index signature or a `Record<string, string>`, turns the same mistake into a
 * heading that renders as the word "undefined", on a page nobody opens until
 * they need it. That is the argument the Go package's own doc comment makes for
 * using a struct instead of a map, and it applies unchanged here.
 *
 * Every field is a string. The Go type is string or template.HTML, and the
 * difference between them is a rendering decision that only existed in
 * html/template — over JSON both arrive as text.
 */
export interface Messages {
  /** The subset app.js reads, served alongside the table. */
  Script: Script

  // ---------------------------------------------------------------- brand
  AppName: string

  // ----------------------------------------------------------- navigation
  NavChannels: string
  NavDeliveries: string
  NavAudit: string
  NavKeys: string
  NavAPI: string
  NavSignOut: string
  /** accessible label for the language switcher */
  NavLangSwitch: string
  /** the first-run checklist, offered only while it is unfinished */
  NavStart: string
  /** accessible label for the switch */
  NavThemeSwitch: string
  /** the theme's name while it is dark */
  ThemeDark: string
  /** the theme's name while it is light */
  ThemeLight: string

  // ---------------------------------------------------------- page titles
  TitleSignIn: string
  TitleSetup: string
  TitleChannels: string
  TitleDeliveries: string
  TitleDelivery: string
  TitleAudit: string
  TitleKeys: string
  TitleAPI: string
  /** the error page, whose heading is the error and not the page */
  TitleError: string
  TitleStart: string

  // ------------------------------------------------ the first-run checklist
  StartIntro: string
  StartStepChannel: string
  StartStepChannelHint: string
  StartStepKey: string
  StartStepKeyHint: string
  StartStepTest: string
  StartStepTestHint: string
  StartStepResult: string
  StartStepResultHint: string
  StartActionGo: string
  StartStepDone: string
  StartDoneHead: string
  StartDoneBody: string

  // ---------------------------------------------------------- shared copy
  CommonSave: string
  CommonCancel: string
  CommonDelete: string
  CommonTest: string
  CommonCopy: string
  CommonDone: string
  CommonCreate: string
  CommonEnable: string
  CommonDisable: string
  CommonFilter: string
  CommonClear: string
  CommonDetail: string
  CommonReplay: string
  CommonBack: string
  CommonNewer: string
  CommonOlder: string
  CommonAny: string
  CommonNever: string
  CommonEnabled: string
  CommonDisabled: string
  /** the required marker's title attribute */
  CommonRequired: string
  /** the key list's source column */
  CommonSetInConfig: string

  // --------------------------------------------------------------- login
  LoginSubtitle: string
  LoginUsername: string
  LoginPassword: string
  LoginSubmit: string

  // --------------------------------------------------------------- setup
  SetupIntro: string
  SetupUsername: string
  SetupPassword: string
  SetupConfirm: string
  SetupSubmit: string
  SetupPasswordRule: string

  // ------------------------------------------------------- change password
  NavPassword: string
  TitlePassword: string
  PasswordIntro: string
  PasswordCurrent: string
  PasswordNew: string
  PasswordConfirm: string
  PasswordSubmit: string
  PasswordRule: string
  /** %d: how many other sessions were signed out */
  PasswordChanged: string

  // ------------------------------------------- the error page's way out
  BackToChannels: string
  BackToDeliveries: string
  BackToAudit: string
  BackToKeys: string
  BackToAPI: string
  BackToSetup: string

  // ------------------------------------------------------------ channels
  /** the header button, and the empty state's action */
  ChannelsNew: string
  ChannelsTableName: string
  ChannelsTableType: string
  ChannelsTableState: string
  ChannelsTableSecrets: string
  ChannelsTagDisabled: string
  ChannelsTagLive: string
  ChannelsTagNotLoaded: string
  ChannelsResetBreaker: string
  ChannelsEmptyTitle: string
  ChannelsEmptyBody: string
  ChannelsEmptyAction: string
  /** %s: the channel's name */
  ChannelFormEditHeading: string
  ChannelFormNameLabel: string
  ChannelFormNameDesc: string
  ChannelFormTypeLabel: string
  /** the type picker's placeholder entry */
  ChannelFormTypeHint: string
  ChannelFormTypeDesc: string
  ChannelFormEnabled: string
  ChannelFormConfigHead: string
  /** a private parameter's placeholder */
  ChannelFormSecretHint: string
  ChannelFormSecretClear: string
  ChannelFormQuotaHead: string
  ChannelFormQuotaDesc: string
  ChannelFormTestConn: string
  ChannelFormListHint: string
  /** %s: the name that was asked for */
  ChannelGoneHead: string
  /** %s: the name that was asked for */
  ChannelGoneBody: string
  TestNotifyButton: string
  TestNotifyHeading: string
  TestNotifyIntro: string
  TestNotifyNeedsRecipient: string
  TestNotifyTitle: string
  TestNotifyTitleHint: string
  TestNotifyBody: string
  TestNotifyBodyHint: string
  TestNotifySubmit: string
  TestNotifyCancel: string

  // ---------------------------------------------------------- deliveries
  /** %d queued, %d sending, %d sent, %d failed */
  DeliveriesStats: string
  DeliveriesFilterStatus: string
  DeliveriesFilterChannel: string
  DeliveriesFilterRequestID: string
  DeliveriesFilterLimit: string
  DeliveriesTableCreated: string
  DeliveriesTableChannel: string
  DeliveriesTableStatus: string
  DeliveriesTableClass: string
  DeliveriesTableAttempts: string
  DeliveriesTableLastError: string
  /** nothing has ever been sent */
  DeliveriesEmptyTitle: string
  DeliveriesEmptyBody: string
  DeliveriesEmptyAction: string
  /** there is a filter and it matched nothing */
  DeliveriesNoMatchTitle: string
  DeliveriesNoMatchBody: string
  DeliveriesNoMatchAction: string
  StatusQueued: string
  StatusSending: string
  StatusSent: string
  StatusFailed: string

  // ------------------------------------------------------- delivery detail
  DeliveryFactID: string
  DeliveryFactRequest: string
  DeliveryFactChannel: string
  DeliveryFactStatus: string
  DeliveryFactAttempts: string
  DeliveryFactCreated: string
  DeliveryFactSent: string
  DeliveryFactNextAttempt: string
  DeliveryFactLastError: string
  /** the body is gone, so it cannot be replayed */
  DeliveryBodyExpired: string
  DeliveryNotDeadLetter: string
  DeliveryAttemptsHeading: string
  DeliveryAttemptWhen: string
  DeliveryAttemptClass: string
  DeliveryAttemptSkipped: string
  DeliveryAttemptDetail: string
  DeliveryAttemptError: string
  /** no attempts recorded */
  DeliveryEmptyTitle: string
  /** names the three reasons a delivery is never attempted */
  DeliveryEmptyBody: string

  // --------------------------------------------------------------- audit
  AuditIntro: string
  AuditTableWhen: string
  AuditTableWho: string
  AuditTableAction: string
  AuditTableChannel: string
  AuditTableDetail: string
  /** this deployment keeps no audit trail */
  AuditDisabledHead: string
  AuditDisabledBody: string
  /** it does, and nothing has happened yet */
  AuditEmptyHead: string
  AuditEmptyBody: string

  // ---------------------------------------------------------------- keys
  KeysIntro: string
  KeysNewHeading: string
  KeysNameLabel: string
  /** the input's placeholder example */
  KeysNameHint: string
  KeysCreateButton: string
  KeysNameDesc: string
  /** %d keys come from the configuration file */
  KeysConfiguredNotice: string
  KeysTableName: string
  KeysTableSource: string
  KeysTableStatus: string
  KeysTableCreated: string
  KeysTableLastUsed: string
  KeysTableRecipients: string
  KeysRecipientsLabel: string
  /** the input's placeholder example */
  KeysRecipientsHint: string
  KeysRecipientsDesc: string
  /** the table cell for a key that may address nobody */
  KeysRecipientsNone: string
  /** the row button */
  KeysRecipientsEdit: string
  /** the edit dialog's heading, then the key's name */
  KeysRecipientsHeading: string
  KeysEmptyTitle: string
  /** names the status code, so it carries <code> */
  KeysEmptyBody: string
  KeysDialogHeading: string
  KeysDialogBody: string
  KeysDialogCopy: string
  KeysDialogDone: string

  // ------------------------------------------------------------ API docs
  APIDocsIntro: string
  APIDocsBaseURL: string
  APIDocsAuth: string
  APIDocsBaseURLNote: string
  APIDocsTokenNote: string
  APIDocsPlainHTTP: string
  APIDocsExamplesHeading: string
  APIDocsExamplesIntro: string
  APIDocsCopy: string
  APIDocsRecipientsHeading: string
  APIDocsRecipientsIntro: string
  APIDocsRecipientsSame: string
  APIDocsRecipientsReplace: string
  APIDocsRecipientsAllowlist: string
  APIDocsRecipientsExclusive: string
  APIDocsRecipientsExampleTo: string
  APIDocsRecipientsExampleURL: string
  APIDocsEndpointsHead: string
  APIDocsTableMethod: string
  APIDocsTablePath: string
  APIDocsTableAuth: string
  APIDocsTablePurpose: string
  APIDocsAuthNone: string
  APIDocsErrorsHeading: string
  APIDocsErrorBodyNote: string
  APIDocsTableStatus: string
  APIDocsTableMeaning: string
  APIDocsGotchasHeading: string
  APIDocsGotchaClassCase: string
  APIDocsGotchaNotAtt: string
  APIDocsSampleNoteCurl: string
  APIDocsSampleNoteGo: string

  // ------------------------------------------------------- relative time
  TimeJustNow: string
  /** %d minutes */
  TimeMinutesAgo: string
  /** %d hours */
  TimeHoursAgo: string
  /** %d days */
  TimeDaysAgo: string

  // ---------------------------------------------------------- error copy
  ErrChannelsUnreadable: string
  ErrChannelUnreadable: string
  ErrChannelNotFound: string
  ErrDeliveriesUnreadable: string
  ErrNoDeliveryStore: string
  ErrDeliveryUnreadable: string
  ErrDeliveryNotFound: string
  ErrAttemptsUnreadable: string
  ErrAuditUnreadable: string
  ErrInvalidJSON: string
  ErrChannelNameRequired: string
  /** %s: the channel's name */
  ErrChannelNameTaken: string
  ErrChannelDeleteFailed: string
  ErrBreakerDisabled: string
  ErrChannelBuildFailed: string
  ErrChannelDisabled: string
  ErrChannelNeedsRecipient: string
  /** %s: the refused pattern */
  ErrKeyRecipientPattern: string
  ErrNoQueue: string
  ErrEnqueueFailed: string
  ErrTestMessageIncomplete: string
  /** %s: the parameter's name (limit, offset) */
  ErrInvalidIntParam: string
  /** %s: the delivery's current status */
  ErrNotReplayableStatus: string
  ErrBodyExpired: string
  ErrReplayFailed: string
  ErrNotReplayable: string
  ErrStatsUnreadable: string
  ErrKeysUnreadable: string
  ErrNoKeyStore: string
  ErrKeyNameRequired: string
  ErrKeyCreateFailed: string
  ErrKeyUnreadable: string
  ErrKeyNotFound: string
  ErrKeyUpdateFailed: string
  ErrKeyDeleteFailed: string
  ErrTokenOnce: string
  ErrAdminUnreadable: string
  ErrTooManyAttempts: string
  ErrUsernameRequired: string
  /** %d: the minimum length */
  ErrPasswordTooShort: string
  ErrNoCredentialStore: string
  ErrAccountCreateFailed: string
  ErrAlreadyConfigured: string
  ErrSessionCreateFailed: string
  ErrTooManySignIns: string
  ErrBadCredentials: string
  ErrSignInFirst: string
  ErrSessionExpired: string
  ErrPasswordInConfig: string
  ErrPasswordChangeFailed: string
  /** %s: the header's name */
  ErrMissingCSRF: string
  ErrNotFound: string
}

/**
 * The confirms and flash messages, shared with the server-rendered pages.
 *
 * Named for the script that reads them there. Both interfaces show the same
 * sentences about the same actions, so they are one table and not two — a
 * delete confirmation that says one thing in the old interface and another in
 * the new one is the kind of difference that makes an operator distrust both.
 */
export interface Script {
  /** %s: the channel's name */
  ConfirmResetBreaker: string
  /** %s: the channel's name */
  ConfirmDeleteChan: string
  /** %s: the API key's name */
  ConfirmDeleteKey: string
  /** %s: the delivery id */
  ConfirmReplay: string
  ConfirmReplace: string
  /** %s: the channel's name */
  SavedFlash: string
  /** %s: what was deleted */
  DeletedFlash: string
  /** %s: the delivery id */
  ReplayedFlash: string
  /** %s: the channel's name, %s: the state it was in */
  BreakerResetFlash: string
  /** %s: why the running service refused it */
  SavedButNotLive: string
  ChooseTypeFirst: string
  SaveBeforeTest: string
  KeyNameRequired: string
  PasswordsNoMatch: string
  /** the first-run form's fallback error */
  SetupFailed: string
  CopyTokenPrompt: string
  CopyManualPrompt: string
  /** %s: what was verified */
  TestOK: string
  /** %s: class, %s: the error */
  TestFailed: string
  TestNotifyQueued: string
  /** %s: the key's name */
  KeyStateEnabled: string
  /** %s: the key's name */
  KeyStateDisabled: string
  KeyRecipientsSaved: string
  DiscardChanges: string
  PasswordSet: string
  FixMarkedFields: string
}

/**
 * The fields whose copy carries markup, generated from the Go type.
 *
 * 13 of the four hundred strings are typed template.HTML there, which
 * means the server-rendered pages interpolate them unescaped — the author wrote
 * <code> and <strong> into them and meant it. Over JSON they arrive as ordinary
 * strings, and React escapes an ordinary string, so without this the interface
 * renders the tags as visible text: a paragraph reading
 * "see <code>docs/07-api.md</code>", angle brackets and all.
 *
 * A generated set rather than a hand-written list, and rather than a flag in
 * the response. The list has to agree with the Go struct exactly, and the one
 * thing a generated list cannot do is drift from it — see the note at the top
 * of this file.
 *
 * Rendering these as markup is safe for the reason the Go templates already
 * rely on: every one of them is a sentence this repository's authors wrote, not
 * anything an operator or an API caller supplied.
 */
export const HTML_FIELDS: ReadonlySet<string> = new Set([
  'KeysEmptyBody',
  'APIDocsIntro',
  'APIDocsTokenNote',
  'APIDocsPlainHTTP',
  'APIDocsExamplesIntro',
  'APIDocsRecipientsIntro',
  'APIDocsRecipientsSame',
  'APIDocsRecipientsReplace',
  'APIDocsRecipientsAllowlist',
  'APIDocsRecipientsExclusive',
  'APIDocsErrorBodyNote',
  'APIDocsGotchaClassCase',
  'APIDocsGotchaNotAtt',
])

/** A message field that carries markup. */
export type HTMLField =
  | 'KeysEmptyBody'
  | 'APIDocsIntro'
  | 'APIDocsTokenNote'
  | 'APIDocsPlainHTTP'
  | 'APIDocsExamplesIntro'
  | 'APIDocsRecipientsIntro'
  | 'APIDocsRecipientsSame'
  | 'APIDocsRecipientsReplace'
  | 'APIDocsRecipientsAllowlist'
  | 'APIDocsRecipientsExclusive'
  | 'APIDocsErrorBodyNote'
  | 'APIDocsGotchaClassCase'
  | 'APIDocsGotchaNotAtt'

/** One language the build ships. */
export interface LangOption {
  tag: string
  label: string
  on: boolean
}

/** What GET /admin/api/i18n answers with. */
export interface I18nPayload {
  lang: string
  langs: LangOption[]
  t: Messages
}
