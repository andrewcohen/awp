package deckui

// deckDeps groups the deck's injected dependencies — the callbacks the
// wiring layer (internal/cli/deck.go) supplies via the WithXxx builder
// methods. They were previously ~24 loose fields scattered through the
// Model struct; collecting them here shrinks the Model definition and
// documents "these are the seams to the rest of the app" as one unit.
//
// deckDeps is embedded in Model, so every existing m.handler / m.refresher
// / … access and every WithXxx setter keeps working unchanged (Go promotes
// embedded fields). Grouping is purely organizational — swapping this for a
// service-backed interface later is a contained change.
type deckDeps struct {
	handler         Handler
	refresher       Refresher
	hookInstaller   HookInstaller
	stateWatcher    StateChangeWatcher
	prFetcher       PRFetcher
	prStatusFetcher PRStatusFetcher
	bookmarkFetcher BookmarkFetcher
	trunkResolver   TrunkResolver
	stateEditor     StateEditorLauncher

	prNumberLinkHandler  PRNumberLinkHandler
	pinGroupHandler      PinGroupHandler
	pinGroupAliasHandler PinGroupAliasHandler
	bookmarkLinkHandler  BookmarkLinkHandler
	userActionsResolver  UserActionsResolver

	projectFinder ProjectFinder
	projectOpener ProjectOpener

	asyncJobLauncher        AsyncJobLauncher
	jobsListRefresher       JobsListRefresher
	jobCancelHandler        JobCancelHandler
	jobDismissHandler       JobDismissHandler
	jobLogOpener            JobLogOpener
	jobRetryHandler         JobRetryHandler
	jobDeleteWorkspaceRetry JobDeleteWorkspaceRetryHandler

	devURLDiscoverer DevURLDiscoverer
}
