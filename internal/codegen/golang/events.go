package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Go event artefacts, laid out along the layers the HTTP side uses:
//
//	<events>/<service>/publisher.go  the typed publisher for a service
//	<events>/<service>/consumers.go  the handler interface + subscriptions
//	<transport>/<service>_consumers.go  the handler set bound to logic
//	<service>/<seg>/<name>.go       the gen-once consumer logic stub
//	<transport>/events.go           SubscribeAll over every consumer
//	<svccontext>/events.go          the Events container main.go fills
//
// The publisher package holds no svccontext import - svccontext holds
// the publishers. The handler set does need svccontext, so it lives in
// the transport layer beside the HTTP handlers. It sits at that layer's
// ROOT rather than in a service's @group folder: a service may declare
// consumers across blocks carrying different groups, and one value has
// to satisfy the single interface the contract declares.

// publisherData is the template input for one service block's publisher.
type publisherData struct {
	Package string
	Service string
	Events  []publisherEvent
	Imports []extraImport
}

// publisherEvent is one Publish<Name> method.
type publisherEvent struct {
	Name        string
	Contract    string
	ConstName   string
	PayloadType string
	Doc         []string
}

// consumersHandlerData is the template input for one service's handler
// set: the type the contract's Consumers interface is satisfied by.
type consumersHandlerData struct {
	Service          string
	Type             string
	Imports          []extraImport
	SvccontextImport string
	Consumers        []consumerAdapter
}

// consumerAdapter is one method on the handler set, forwarding a payload
// the contract already decoded and validated to that consumer's logic.
type consumerAdapter struct {
	Name        string
	Contract    string
	PayloadType string
	// ServiceAlias is the import alias of the logic package holding this
	// consumer's stub - per @group, so one handler set may reach several.
	ServiceAlias string
	Doc          []string
}

// consumersData is the template input for a service's library-side
// consumer API: the handler interface plus its subscription builder.
type consumersData struct {
	Package string
	Service string
	Imports []extraImport
	// Middlewares is every consume middleware this service's consumers
	// name, sorted, one field on the generated Middlewares struct. Empty
	// leaves both that struct and the third parameter out, so a service
	// that declares no chain keeps the signature it already published.
	Middlewares []string
	Consumers   []consumersEntry
}

// consumersEntry is one contract a service consumes.
type consumersEntry struct {
	Name           string
	QuotedName     string
	QuotedContract string
	QuotedGroup    string
	PayloadType    string
	Doc            []string
	// ChainArgs is this consumer's declared chain rendered as the
	// variadic tail of the wrap call (`mw.DeadLetter, mw.Retry`),
	// outermost first. Empty means no chain, and the subscription is
	// emitted bare.
	ChainArgs string
}

// consumerStubData is the template input for a gen-once consumer stub.
type consumerStubData struct {
	Package          string
	Service          string
	Name             string
	ConsumerType     string
	Contract         string
	PayloadType      string
	Doc              []string
	Imports          []extraImport
	SvccontextImport string
}

// eventsAllData is the template input for the project-wide events.go.
type eventsAllData struct {
	SvccontextImport string
	Imports          []extraImport
	Services         []consumerHandlerEntry
	// Guards is one startup check per consume middleware the design
	// applies. They live here rather than in wiring.Register because
	// SubscribeAll is the call every consumer deployable makes - one that
	// owns no HTTP server has no reason to call Register at all.
	Guards []middlewareGuard
}

// consumerHandlerEntry is one consuming service in SubscribeAll: its event
// package, which builds the subscriptions, and the handler set it binds.
type consumerHandlerEntry struct {
	Alias string
	Type  string
	// MiddlewareArg is the third argument to that service's
	// Subscriptions, a composite literal binding each declared field to
	// its value on the ServiceContext. Empty when the service declares no
	// chain, and the call stays two arguments wide.
	MiddlewareArg string
}

// eventsFieldsData is the template input for svccontext/events.go.
type eventsFieldsData struct {
	Imports    []extraImport
	Publishers []publisherEntry
	// ConsumeMiddlewares is every `consume middleware Name` the design
	// declares, sorted: one field on the generated ConsumeMiddlewares
	// struct that main.go assigns at startup.
	ConsumeMiddlewares []string
}

type publisherEntry struct {
	Field string
	Alias string
	// FirstEvent names one of the service's contracts, used in the batch
	// builder's doc comment so the example compiles against real names.
	FirstEvent string
}

// generatePackageContracts emits pkg's contract half: the publisher and
// the consumer subscriptions, both under libraryRoot. outDir is the Go
// event target directory.
func generatePackageContracts(pkg *semantic.Package, cfg *config.Config, libraryRoot, outDir string, r *projectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	r = resolverFor(pkg, r)
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if len(r.Proj.PublishedBy(pkg, svcName)) == 0 && len(svc.Consumers) == 0 {
			continue
		}
		if err := writePublisher(pkg, svcName, svc, cfg, libraryRoot, outDir, r); err != nil {
			return err
		}
		if err := writeConsumerAPI(pkg, svcName, svc, cfg, libraryRoot, outDir, r); err != nil {
			return err
		}
	}
	return nil
}

// generatePackageConsumers emits the application half of pkg's event
// model: the adapter binding each consumer to the container, and the
// gen-once logic stub behind it.
func generatePackageConsumers(pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	r = resolverFor(pkg, r)
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if len(svc.Consumers) == 0 {
			continue
		}
		for _, group := range consumerGroupSet(svc) {
			if err := writeConsumers(pkg, svcName, svc, group, cfg, projectRoot, r); err != nil {
				return err
			}
		}
		if err := writeConsumerHandler(pkg, svcName, svc, cfg, projectRoot, r); err != nil {
			return err
		}
	}
	return nil
}

// consumerGroupSet returns the @group values under which a service
// declares consumers, in deterministic order. Consumer stubs sit beside
// the method stubs of the block that declared them, so they follow
// @group; a publisher does not, because it has no per-member file.
func consumerGroupSet(svc *semantic.ServiceInfo) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range consumerGroups(svc) {
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

// publisherDir returns the directory a service's publisher is written
// to: always the service's own, never a `@group`. `@group` exists to
// merge several services' per-member handler files into one folder; a
// publisher is one file for a whole service, so two grouped services
// would claim the same filename and the same `Publisher` type.
func publisherDir(libraryRoot, outDir, svcName, fileCase string) string {
	return serviceOutputDir(libraryRoot, outDir, svcName, "", fileCase)
}

// writePublisher emits one service's publisher.go, carrying every event
// the service declares across all its blocks.
func writePublisher(pkg *semantic.Package, svcName string, svc *semantic.ServiceInfo, cfg *config.Config, libraryRoot, outDir string, r *projectResolver) error {
	imps := importPathsForGroup(cfg, pkg, svcName, "")
	imports := newImportSet(r.CrossPkg)
	data := publisherData{Package: servicePkgName(pkg.Name, svcName), Service: svcName}
	for _, ed := range svc.Events {
		pe, err := buildPublisherEvent(pkg, ed, r, imports, imps.Types)
		if err != nil {
			return err
		}
		data.Events = append(data.Events, pe)
	}
	if len(data.Events) == 0 {
		return nil
	}
	data.Imports = imports.sorted()
	dir := publisherDir(libraryRoot, outDir, svcName, cfg.Output.FileCase)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	formatted, err := renderGo(tmpl("publisher.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render publisher.go: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "publisher.go"), formatted, 0o644)
}

// buildPublisherEvent shapes one Publish<Name> method.
func buildPublisherEvent(pkg *semantic.Package, ed *ast.EventDecl, r *projectResolver, imports *importSet, typesImport string) (publisherEvent, error) {
	ev, ok := r.Proj.LookupEvent(pkg.Name, ed.Name)
	if !ok {
		return publisherEvent{}, fmt.Errorf("event %q did not resolve", ed.Name)
	}
	pe := publisherEvent{
		Name:      ed.Name,
		Contract:  ev.Contract,
		ConstName: ed.Name + "Contract",
		Doc:       ed.Doc,
	}
	pe.PayloadType = imports.payloadRefType(ev.PayloadRef, typesImport)
	return pe, nil
}

// consumerEventRef returns the event name a consumer references.
func consumerEventRef(c *ast.ConsumerDecl) string {
	if c == nil || c.Event == nil || c.Event.Ref == nil || c.Event.Ref.Name == nil {
		return ""
	}
	return c.Event.Ref.Name.String()
}

// writeConsumers scaffolds the gen-once logic stub for every consumer of
// one @group. The stub sits beside the method stubs of the block that
// declared it, which is what @group is for; the handler binding it to the
// bus is written once per service by [writeConsumerHandler].
func writeConsumers(pkg *semantic.Package, svcName string, svc *semantic.ServiceInfo, group string, cfg *config.Config, projectRoot string, r *projectResolver) error {
	groups := consumerGroups(svc)
	imps := importPathsForGroup(cfg, pkg, svcName, group)
	pkgName := servicePkgName(pkg.Name, svcName)
	for _, cd := range svc.Consumers {
		if groups[cd.Name] != group {
			continue
		}
		ev, ok := r.Proj.LookupEvent(pkg.Name, consumerEventRef(cd))
		if !ok {
			continue
		}
		stubImports := newImportSet(r.CrossPkg)
		stub := consumerStubData{
			Package:          pkgName,
			Service:          svcName,
			Name:             cd.Name,
			ConsumerType:     cd.Name + "Consumer",
			Contract:         ev.Contract,
			PayloadType:      stubImports.payloadType(ev, imps.Types, cfg),
			Doc:              cd.Doc,
			SvccontextImport: imps.Svccontext,
		}
		stub.Imports = stubImports.sorted()
		stubDir := serviceOutputDir(projectRoot, cfg.Output.Service, svcName, group, cfg.Output.FileCase)
		if err := scaffoldRendered(stubDir, idents.FileName(cd.Name, cfg.Output.FileCase)+".go", "consumer.tmpl", stub); err != nil {
			return err
		}
	}
	return nil
}

// writeConsumerHandler emits one service's handler set at the root of the
// transport output: the type satisfying the Consumers interface the
// service's event package declares, plus one forwarding method per
// consumer. It covers every @group the service spreads its consumers
// over, because the contract declares one interface for all of them.
func writeConsumerHandler(pkg *semantic.Package, svcName string, svc *semantic.ServiceInfo, cfg *config.Config, projectRoot string, r *projectResolver) error {
	groups := consumerGroups(svc)
	imports := newImportSet(r.CrossPkg)
	data := consumersHandlerData{
		Service:          svcName,
		Type:             consumersTypeName(svcName),
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
	for _, cd := range svc.Consumers {
		ev, ok := r.Proj.LookupEvent(pkg.Name, consumerEventRef(cd))
		if !ok {
			continue
		}
		imps := importPathsForGroup(cfg, pkg, svcName, groups[cd.Name])
		imports.add(extraImport{Alias: consumerLogicAlias(svcName, groups[cd.Name]), Path: imps.Service})
		data.Consumers = append(data.Consumers, consumerAdapter{
			Name:         cd.Name,
			Contract:     ev.Contract,
			PayloadType:  imports.payloadType(ev, imps.Types, cfg),
			ServiceAlias: imports.aliasFor(imps.Service),
			Doc:          cd.Doc,
		})
	}
	if len(data.Consumers) == 0 {
		return nil
	}
	data.Imports = imports.sorted()
	return writeRendered(filepath.Join(projectRoot, cfg.Output.Transport),
		consumersFileName(svcName, cfg.Output.FileCase), "consumers-handler.tmpl", data)
}

// writeRendered renders tmplName into dir/name, overwriting any existing
// file.
func writeRendered(dir, name, tmplName string, data any) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	formatted, err := renderGo(tmpl(tmplName), data)
	if err != nil {
		return fmt.Errorf("render %s: %w", name, err)
	}
	return os.WriteFile(filepath.Join(dir, name), formatted, 0o644)
}

// scaffoldRendered is [writeRendered] for gen-once files: an existing
// file is left untouched so user-written logic survives regeneration.
func scaffoldRendered(dir, name, tmplName string, data any) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		return nil
	}
	return writeRendered(dir, name, tmplName, data)
}

// generateSvccontextEvents writes the svccontext Events container. It runs
// for every project, including one whose design declares no event: main.go
// is generated once and may already call [NewEvents], so the type has to
// outlive the last event the design declares.
func generateSvccontextEvents(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	if cfg.Output.ContractsOnly() {
		return nil
	}
	outDir := ""
	if target, ok := cfg.Events.TargetFor(config.LangGo); ok {
		outDir = target.Out
	}
	fields := eventsFieldsData{}
	publisherImports := map[string]string{}
	if eventsEnabled(proj, cfg) {
		for _, pkgName := range sortedKeys(proj.Packages) {
			pkg := proj.Packages[pkgName]
			if pkg == nil || pkgName == "" {
				continue
			}
			for _, svcName := range sortedServices(pkg) {
				published := proj.PublishedBy(pkg, svcName)
				if len(published) == 0 {
					continue
				}
				alias := eventsAlias(svcName)
				publisherImports[alias] = eventsPkgImport(cfg, outDir, svcName)
				fields.Publishers = append(fields.Publishers, publisherEntry{
					Field: svcName, Alias: alias, FirstEvent: published[0].Name,
				})
			}
		}
	}
	// A design with no event names no event type, so the project takes on
	// no dependency for one.
	tmplName := "events-fields-empty.tmpl"
	if eventsEnabled(proj, cfg) {
		tmplName = "events-fields.tmpl"
	}
	fields.ConsumeMiddlewares = projectSortedConsumeMiddlewareNames(proj)
	fields.Imports = importsFrom(publisherImports)
	return writeRendered(filepath.Join(projectRoot, fileDirRel(cfg.Output.Svccontext)), "events.go", tmplName, fields)
}

// generateProjectEvents writes the project-wide SubscribeAll and clears the
// handler set of any service that no longer consumes anything.
func generateProjectEvents(proj *semantic.Project, cfg *config.Config, projectRoot, outDir string) error {
	_, consumeGuards := middlewareGuards(proj, cfg.Output.RuntimeDisabled())
	all := eventsAllData{
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
		Guards:           consumeGuards,
	}
	eventsImports := map[string]string{}
	written := map[string]bool{}
	var unwritten []string
	for _, pkgName := range sortedKeys(proj.Packages) {
		pkg := proj.Packages[pkgName]
		if pkg == nil || pkgName == "" {
			continue
		}
		for _, svcName := range sortedServices(pkg) {
			file := consumersFileName(svcName, cfg.Output.FileCase)
			if !consumesAnything(proj, pkg, pkg.Services[svcName]) {
				unwritten = append(unwritten, file)
				continue
			}
			written[file] = true
			alias := eventsAlias(svcName)
			eventsImports[alias] = eventsPkgImport(cfg, outDir, svcName)
			all.Services = append(all.Services, consumerHandlerEntry{
				Alias:         alias,
				Type:          consumersTypeName(svcName),
				MiddlewareArg: consumeMiddlewareArg(alias, proj, pkg, svcName),
			})
		}
	}
	// Sweep after the whole project is known: two service names may fold to
	// one file, and a non-consuming one must not delete a sibling's handlers.
	for _, file := range unwritten {
		if !written[file] {
			removeGenerated(filepath.Join(projectRoot, cfg.Output.Transport, file))
		}
	}
	transportEvents := filepath.Join(projectRoot, cfg.Output.Transport, "events.go")
	if len(all.Services) == 0 {
		removeGenerated(transportEvents)
		return nil
	}
	all.Imports = importsFrom(eventsImports)
	// SubscribeAll is application wiring - it reaches into the per-service
	// transport packages and the ServiceContext - so it sits at the root of
	// the transport output, which exists whenever a consumer does. Emitting
	// it into the events output would make that directory import the
	// application back, and the event artefacts are meant to stand alone.
	// The routes umbrella is not an option: an events-only design writes no
	// routes package at all.
	return writeRendered(filepath.Join(projectRoot, cfg.Output.Transport), "events.go", "events-all.tmpl", all)
}

// importsFrom turns an alias→path map into the sorted import list the
// templates range over.
func importsFrom(m map[string]string) []extraImport {
	out := make([]extraImport, 0, len(m))
	for _, alias := range sortedKeys(m) {
		out = append(out, extraImport{Alias: alias, Path: m[alias]})
	}
	return out
}

// eventsAlias is the import alias svccontext/events.go uses for one
// service's publisher package.
func eventsAlias(svcName string) string {
	return escapeReserved(strings.ToLower(svcName) + "events")
}

// consumerLogicAlias is the import alias a handler set uses for the logic
// package of one of its consumers. Keyed by @group as well as service:
// consumers a group moved elsewhere live in a package of their own, and
// every one of them is imported into the same file.
func consumerLogicAlias(svcName, group string) string {
	return escapeReserved(strings.ToLower(svcName+groupAliasSuffix(group)) + "logic")
}

// consumersTypeName is the handler-set type a service's Consumers
// interface is satisfied by.
func consumersTypeName(svcName string) string { return svcName + "Consumers" }

// consumesAnything reports whether a service declares a consumer whose
// event resolves - the condition under which [writeConsumerAPI] emits the
// Subscriptions builder SubscribeAll calls.
func consumesAnything(proj *semantic.Project, pkg *semantic.Package, svc *semantic.ServiceInfo) bool {
	if svc == nil {
		return false
	}
	for _, cd := range svc.Consumers {
		if _, ok := proj.LookupEvent(pkg.Name, consumerEventRef(cd)); ok {
			return true
		}
	}
	return false
}

// consumeMiddlewareArg renders the Middlewares literal SubscribeAll hands
// a service, binding each field the service's consumers name to its value
// on the ServiceContext. The field set is exactly the one
// [writeConsumerAPI] emits, so the literal and the struct cannot drift.
func consumeMiddlewareArg(alias string, proj *semantic.Project, pkg *semantic.Package, svcName string) string {
	svc := pkg.Services[svcName]
	if svc == nil {
		return ""
	}
	used := map[string]struct{}{}
	for _, cd := range svc.Consumers {
		rc := proj.ResolveConsumer(pkg, svcName, cd)
		if rc.Event.Contract == "" {
			continue
		}
		for _, n := range consumeMiddlewareNames(cd, svc.Primary) {
			used[n] = struct{}{}
		}
	}
	names := sortedKeys(used)
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + ": svcCtx.Events.Consume." + n
	}
	return alias + ".Middlewares{" + strings.Join(parts, ", ") + "}"
}

// chainArgs renders a consumer's chain as the variadic tail of the
// generated wrap call, outermost first - the order the design writes and
// the order the message flows through. Empty chain, empty string, and the
// subscription is emitted without a wrap.
func chainArgs(names []string) string {
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = "mw." + n
	}
	return strings.Join(parts, ", ")
}

// escapeReserved renames an alias that collides with an identifier the
// event templates already bind, the way [importSet.aliasFor] does for
// payload packages. A service named `Craft` publishes through
// `craftevents`, which is the alias the runtime import holds, so the two
// import lines would declare the same name twice.
func escapeReserved(alias string) string {
	candidate := alias
	for i := 2; reservedAliases[candidate]; i++ {
		candidate = fmt.Sprintf("%s%d", alias, i)
	}
	return candidate
}

// writeConsumerAPI emits the service's library-side consumer API next to
// its publisher: a handler interface the importer implements and a
// subscription builder. It imports only the event runtime and the payload
// types, so the contract output stays importable on its own - the app-side
// wiring lives in the transport package instead. Like the publisher it is
// one file per service and ignores @group.
func writeConsumerAPI(pkg *semantic.Package, svcName string, svc *semantic.ServiceInfo, cfg *config.Config, libraryRoot, outDir string, r *projectResolver) error {
	imps := importPathsForGroup(cfg, pkg, svcName, "")
	imports := newImportSet(r.CrossPkg)
	data := consumersData{Package: servicePkgName(pkg.Name, svcName), Service: svcName}
	used := map[string]struct{}{}
	for _, cd := range svc.Consumers {
		rc := r.Proj.ResolveConsumer(pkg, svcName, cd)
		if rc.Event.Contract == "" {
			continue
		}
		chain := consumeMiddlewareNames(cd, svc.Primary)
		for _, n := range chain {
			used[n] = struct{}{}
		}
		data.Consumers = append(data.Consumers, consumersEntry{
			Name:           rc.Name,
			QuotedName:     strconv.Quote(rc.Name),
			QuotedContract: strconv.Quote(rc.Event.Contract),
			QuotedGroup:    strconv.Quote(rc.Group),
			PayloadType:    imports.payloadType(rc.Event, imps.Types, cfg),
			Doc:            rc.Doc,
			ChainArgs:      chainArgs(chain),
		})
	}
	data.Middlewares = sortedKeys(used)
	if len(data.Consumers) == 0 {
		return nil
	}
	data.Imports = imports.sorted()
	dir := publisherDir(libraryRoot, outDir, svcName, cfg.Output.FileCase)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	formatted, err := renderGo(tmpl("consumers.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render consumers.go: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "consumers.go"), formatted, 0o644)
}
