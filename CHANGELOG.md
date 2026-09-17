# Changelog

## v256

Refreshes Playground chat with a responsive layout, editable prompts, sampling
controls, and live generation statistics across Chat and the Help agent. Adds a
global inference concurrency cap, richer TabbyAPI activity metrics, leaner zstd
memory use, and a storage-layer refactor, alongside release-changelog tooling.

- CHANGELOG.md,docs: fix pull request link
- [PR #1125](https://github.com/mostlygeek/llama-swap/pull/1125) changelog: add scripts and automation to generate changelog entries
- [PR #1120](https://github.com/mostlygeek/llama-swap/pull/1120) ui: make the playground chat responsive and lighter
- [PR #1123](https://github.com/mostlygeek/llama-swap/pull/1123) internal/server: reduce zstd pool memory consumption
- [PR #1122](https://github.com/mostlygeek/llama-swap/pull/1122) internal/store: split repository interfaces from the sqlite backend
- [PR #1095](https://github.com/mostlygeek/llama-swap/pull/1095) process: start TTL idle window when model becomes ready
- [PR #1110](https://github.com/mostlygeek/llama-swap/pull/1110) internal/server: add global concurrency semaphore
- cmd/vllm-wrapper: use default timeouts
- [PR #1104](https://github.com/mostlygeek/llama-swap/pull/1104) internal/server: add tabbyapi usage data extraction
- ui: show chat stats in Help agent
- [PR #1099](https://github.com/mostlygeek/llama-swap/pull/1099) ui: show live generation stats in Chat
- [PR #1101](https://github.com/mostlygeek/llama-swap/pull/1101) ui: keep Work status bar stable while streaming

## v255

Updates Tailcat to v0.6.0, including WireGuard preshared-key support for
Tailcat addresses.

- [PR #1098](https://github.com/mostlygeek/llama-swap/pull/1098) tailcat: update to v0.6.0

## v254

Adds CUDA 13 and arm64 support to unified Docker images, configurable build
targets, safer Tailcat configuration sharing, and expanded Help-agent limits.

- [PR #1093](https://github.com/mostlygeek/llama-swap/pull/1093) docker/unified: add cuda13 with multi-platform support for arm64
- [PR #1075](https://github.com/mostlygeek/llama-swap/pull/1075) filters: set-if-undefined params via "?" key suffix
- [PR #1091](https://github.com/mostlygeek/llama-swap/pull/1091) ui/tailcat: mask connection token, add shareable peer config
- [PR #1089](https://github.com/mostlygeek/llama-swap/pull/1089) ui/help: raise Docs Agent max_tokens from 4096 to 65536

## v253

Refines the Help experience, adds jq queries for config paths, introduces
private Tailcat peers, and makes unified CUDA builds configurable.

- [PR #1088](https://github.com/mostlygeek/llama-swap/pull/1088) ui/help: refine Help agent topics and controls
- [PR #1087](https://github.com/mostlygeek/llama-swap/pull/1087) Replace config path argument with jq query support
- [PR #1074](https://github.com/mostlygeek/llama-swap/pull/1074) tailcat: add private server and peer connectivity
- [PR #1072](https://github.com/mostlygeek/llama-swap/pull/1072) docker/unified: make CUDA version and architectures configurable

## v252

Speeds unified Docker builds, expands documentation and configuration tooling,
and improves model, WOL proxy, modality, and chat-input support.

- [PR #1071](https://github.com/mostlygeek/llama-swap/pull/1071) Split Docker build into per-project stages for CI
- [PR #1070](https://github.com/mostlygeek/llama-swap/pull/1070) ui: improve Help page
- [PR #1054](https://github.com/mostlygeek/llama-swap/pull/1054) internal/reference: add indexed docs and /api/tools endpoints
- [PR #1053](https://github.com/mostlygeek/llama-swap/pull/1053) config,server: add startup profile hook
- [PR #1063](https://github.com/mostlygeek/llama-swap/pull/1063) expose context_window on models endpoint
- [PR #1055](https://github.com/mostlygeek/llama-swap/pull/1055) cmd/wol-proxy: add -api-key option for use in health checks
- [PR #1057](https://github.com/mostlygeek/llama-swap/pull/1057) cmd/wol-proxy: add liveness check
- [PR #1049](https://github.com/mostlygeek/llama-swap/pull/1049) Typo
- [PR #1048](https://github.com/mostlygeek/llama-swap/pull/1048) config: accept video as an input and output modality
- [PR #1046](https://github.com/mostlygeek/llama-swap/pull/1046) cli: add -validate flag to check config and exit
- [PR #1045](https://github.com/mostlygeek/llama-swap/pull/1045) ui: ignore IME composition Enter in keydown handlers

## v251

Records disconnected clients accurately, improves server and vLLM metrics, and
adds deployment, log-rendering, and activity-export improvements.

- [PR #1040](https://github.com/mostlygeek/llama-swap/pull/1040) internal: record client disconnects as 499, not 200 or 502
- [PR #1043](https://github.com/mostlygeek/llama-swap/pull/1043) internal/server: rate limit sendBuffer full warnings
- [PR #1039](https://github.com/mostlygeek/llama-swap/pull/1039) internal/server: parse vLLM speculative decoding metrics
- [PR #1038](https://github.com/mostlygeek/llama-swap/pull/1038) internal/swaputil: OpenAI-compatible error bodies
- [PR #1023](https://github.com/mostlygeek/llama-swap/pull/1023) docker/unified: build audio.cpp as a deployment build
- [PR #1019](https://github.com/mostlygeek/llama-swap/pull/1019) ui: render ANSI colors in logs
- [PR #1020](https://github.com/mostlygeek/llama-swap/pull/1020) ui-svelte: Add markdown activity export

## v250

Adds audio.cpp, llama-bench, and vLLM-wrapper to the unified image, alongside
audio task APIs, model metadata, and argv-based vLLM startup support.

- [PR #1011](https://github.com/mostlygeek/llama-swap/pull/1011) docker/unified: add llama-bench, vllm-wrapper, audio.cpp
- README.md: reorder list of features
- [PR #979](https://github.com/mostlygeek/llama-swap/pull/979) Support argv-based vLLM startup in vllm-wrapper
- [PR #1007](https://github.com/mostlygeek/llama-swap/pull/1007) ui-svelte,internal/server: show capability tags on Models page
- [PR #982](https://github.com/mostlygeek/llama-swap/pull/982) api: support /v1/task/run for audio.cpp
- AGENTS.md: tweak rules around pull requests
- CONTRIBUTING.md: update rules
- [PR #984](https://github.com/mostlygeek/llama-swap/pull/984) expose meta.n_ctx on models endpoint
- [PR #983](https://github.com/mostlygeek/llama-swap/pull/983) router: add /models endpoint

## v249

Adds a ComfyUI compatibility endpoint and reorganizes configuration and shared
package naming.

- [PR #1003](https://github.com/mostlygeek/llama-swap/pull/1003) various: improvements to code layout and naming
- [PR #1002](https://github.com/mostlygeek/llama-swap/pull/1002) internal/server: add ComfyUI compatibility endpoint

## v248

Preserves percent-encoded paths when proxying requests upstream.

- [PR #988](https://github.com/mostlygeek/llama-swap/pull/988) internal/server,shared: preserve percent encoded in upstream

## v247

Updates the Svelte UI for security and adds inference-host hardware detection.

- ui-svelte: security update
- [PR #978](https://github.com/mostlygeek/llama-swap/pull/978) internal/hw: detect inference host hardware

## v246

Displays configured selectors in the Models page and improves ROCm GPU-memory
reporting and Vulkan image diagnostics.

- [PR #975](https://github.com/mostlygeek/llama-swap/pull/975) ui-svelte: show configured selectors and strategies on Models page
- [PR #973](https://github.com/mostlygeek/llama-swap/pull/973) internal/perf: fix rocm-smi GPU memory utilization
- [PR #968](https://github.com/mostlygeek/llama-swap/pull/968) docker: install rocm-smi for vulkan backend

## v245

Adds profile details to the Models page and supports watching Docker
configuration changes.

- [PR #966](https://github.com/mostlygeek/llama-swap/pull/966) ui-svelte: show profiles on model page
- [PR #964](https://github.com/mostlygeek/llama-swap/pull/964) AGENTS.md,CONTRIBUTING.md: update contribution guidelines
- [PR #963](https://github.com/mostlygeek/llama-swap/pull/963) docker: add -watch-config

## v244

Reworks matrix evaluation to solve expressions symbolically, greatly improving
performance for large configurations.

- [PR #960](https://github.com/mostlygeek/llama-swap/pull/960) internal/matrix: solve matrix expressions symbolically

## v243

Relaxes matrix reference constraints and adds a vLLM-wrapper helper for model
sleep and wake operations.

- [PR #957](https://github.com/mostlygeek/llama-swap/pull/957) internal/config: relax matrix model reference constraints
- [PR #941](https://github.com/mostlygeek/llama-swap/pull/941) cmd/vllm-wrapper: add helper for sleep/wake
- [PR #955](https://github.com/mostlygeek/llama-swap/pull/955) internal/config: refactor macro expansion

## v242

Adds namespaced peer-model support throughout routing and the UI, fixes a
concurrent process-start race, and removes the audio upload size limit.

- [PR #950](https://github.com/mostlygeek/llama-swap/pull/950) internal/server,ui-svelte: add peer model namespaces
- [PR #949](https://github.com/mostlygeek/llama-swap/pull/949) Fix process start race during concurrent stop operations
- [PR #948](https://github.com/mostlygeek/llama-swap/pull/948) ui-svelte: remove audio file size limit

## v241

Introduces selector strategies and profiles, streamlines model request handling,
supports more audio voice formats, and adds FFmpeg to the Docker image.

- [PR #942](https://github.com/mostlygeek/llama-swap/pull/942) internal/server: implement selectors
- [PR #940](https://github.com/mostlygeek/llama-swap/pull/940) internal/server,shared: reduce request model body functions to one
- [PR #935](https://github.com/mostlygeek/llama-swap/pull/935) internal/server: add support for profiles
- [PR #932](https://github.com/mostlygeek/llama-swap/pull/932) ui-svelte: add support for different v1/audio/voices response formats
- [PR #785](https://github.com/mostlygeek/llama-swap/pull/785) docker: add FFmpeg support for whisper.cpp

## v240

Expands inflight request details, adds configurable unload timeouts, and
reduces UI bundle sizes while preserving YAML capability anchors.

- [PR #923](https://github.com/mostlygeek/llama-swap/pull/923) internal/server: expand inflight request details
- [PR #904](https://github.com/mostlygeek/llama-swap/pull/904) Add configurable UnloadTimeout variable
- [PR #925](https://github.com/mostlygeek/llama-swap/pull/925) ui-svelte: code-split routes and trim highlight.js/chart.js bundles
- .coderabbit.yaml: disable annoying unit test creation
- [PR #918](https://github.com/mostlygeek/llama-swap/pull/918) internal/config: preserve yaml anchors in capabilities

## v239

Preserves YAML anchors in capability configuration.

- internal/config: preserve yaml anchors in capabilities

## v238

Improves vLLM response metrics and resolves macros in capability fields.

- [PR #913](https://github.com/mostlygeek/llama-swap/pull/913) internal/server: improve vllm response metric calculation
- [PR #907](https://github.com/mostlygeek/llama-swap/pull/907) internal/config: resolve macros in capabilities fields

## v237

Adds vLLM metrics, timestamps older activity logs, and removes WOL proxy
restart lag.

- [PR #911](https://github.com/mostlygeek/llama-swap/pull/911) ui-svelte: activity logs older than a day show a timestamp
- [PR #910](https://github.com/mostlygeek/llama-swap/pull/910) internal/server: support vLLM metrics
- [PR #909](https://github.com/mostlygeek/llama-swap/pull/909) cmd/wol-proxy: remove lag when llama-swap restarts
- Remove Star History section from README

## v236

Adds llama-server model status and persists activity metrics in SQLite.

- [PR #901](https://github.com/mostlygeek/llama-swap/pull/901) internal/server: add status to v1/models for llama-server
- [PR #898](https://github.com/mostlygeek/llama-swap/pull/898) internal/store: persist activity metrics to sqlite
- fix starhistory

## v235

Shows inflight activity requests, adds llama-tts to builds, and improves build
version stamping.

- [PR #895](https://github.com/mostlygeek/llama-swap/pull/895) internal/server: show inflight activity requests
- [PR #894](https://github.com/mostlygeek/llama-swap/pull/894) Add llama-tts binary
- [PR #891](https://github.com/mostlygeek/llama-swap/pull/891) Makefile: improve build version stamping

## v234

Rejects excess concurrent requests before streaming and adds model route
properties.

- [PR #889](https://github.com/mostlygeek/llama-swap/pull/889) internal/router: reject concurrency excess before streaming
- [PR #886](https://github.com/mostlygeek/llama-swap/pull/886) add /props to modelGetRoutes

## v233

Decouples log broadcasting from writes and makes UI embedding opt-in at build
time.

- [PR #878](https://github.com/mostlygeek/llama-swap/pull/878) internal/logmon: decouple log broadcast from Write
- [PR #880](https://github.com/mostlygeek/llama-swap/pull/880) internal/server: gate UI embed behind embed_ui build tag

## v232

Refreshes the UI with rounded borders and an updated hero image.

- ui-svelte: add rounded borders
- update hero image

## v231

Adds the shadcn-svelte UI foundation, theming, and refreshed screenshots.

- Update screenshots
- [PR #877](https://github.com/mostlygeek/llama-swap/pull/877) ui: add shadcn-svelte foundation and theming
- AGENTS.md: small tweaks

## v230

Adds a configurable configuration-directory option and watcher support.

- [PR #873](https://github.com/mostlygeek/llama-swap/pull/873) internal/config,watcher: add -config-dir

## v229

Adds ignored upstream paths, failed-request captures, and activity columns,
with improved performance-menu visibility.

- [PR #869](https://github.com/mostlygeek/llama-swap/pull/869) config,server: add upstream.ignorePaths
- [PR #832](https://github.com/mostlygeek/llama-swap/pull/832) feat: hide performance menu item if disabled
- [PR #862](https://github.com/mostlygeek/llama-swap/pull/862) server: capture failed (non-200) LLM requests
- [PR #859](https://github.com/mostlygeek/llama-swap/pull/859) internal/server,ui: add new Activity page column - Drafted

## v228

Meters /upstream requests through the metrics middleware.

- [PR #858](https://github.com/mostlygeek/llama-swap/pull/858) proxy: meter /upstream requests via metrics middleware

## v227

Adds experimental Windows GPU monitoring, request metadata, scheduler-based
concurrency limits, and a MacPorts installation option.

- [PR #779](https://github.com/mostlygeek/llama-swap/pull/779) perf: add vendor-agnostic GPU monitoring for Windows (experimental)
- [PR #850](https://github.com/mostlygeek/llama-swap/pull/850) internal/server,shared: support request metadata
- .coderabbit.yaml: disable unit_tests
- [PR #849](https://github.com/mostlygeek/llama-swap/pull/849) schedule,shared: move concurrency 429 limits into scheduler code
- [PR #848](https://github.com/mostlygeek/llama-swap/pull/848) README.md: add macports install option to README

## v226

Improves manual model loading and cancellation in the UI.

- [PR #847](https://github.com/mostlygeek/llama-swap/pull/847) ui: improve manual model load and cancel

## v225

Adds model capabilities and a new scheduler, while refactoring authentication
and showing a warning for network listeners.

- [PR #842](https://github.com/mostlygeek/llama-swap/pull/842) Model capabilities 734
- [PR #839](https://github.com/mostlygeek/llama-swap/pull/839) internal/router,server,shared: refactor auth, libs
- main: gofmt
- [PR #836](https://github.com/mostlygeek/llama-swap/pull/836) main: show message when listening on network
- [PR #823](https://github.com/mostlygeek/llama-swap/pull/823) Implement new scheduler

## v224

Fixes websocket and unload regressions, removes legacy proxy code, and improves
Docker arm64 downloads.

- [PR #830](https://github.com/mostlygeek/llama-swap/pull/830) Makefile,internal: fix websocket regression and other small things
- [PR #828](https://github.com/mostlygeek/llama-swap/pull/828) internal/process,server: fix unload regression
- [PR #822](https://github.com/mostlygeek/llama-swap/pull/822) proxy: remove legacy code
- [PR #819](https://github.com/mostlygeek/llama-swap/pull/819) docker: fix arm64 cpu image downloading amd64 llama-swap binary
- Change cron schedule for container builds

## v223

Adds macOS GPU monitoring using mactop and ioreg.

- [PR #816](https://github.com/mostlygeek/llama-swap/pull/816) perf: add macOS GPU monitoring via mactop and ioreg

## v222

Improves Windows process shutdown behavior.

- [PR #808](https://github.com/mostlygeek/llama-swap/pull/808) internal/process: improve windows shutdown behaviour

## v221

Makes model shutdown and load streaming more robust.

- process,router: make model shutdown and load-streaming robust

## v220

Adds a UI load-testing tool and returns JSON responses for concurrency limits.

- [PR #805](https://github.com/mostlygeek/llama-swap/pull/805) Add load testing tool to the UI
- [PR #798](https://github.com/mostlygeek/llama-swap/pull/798) fix: update the concurrency middleware to respond with a JSON payload

## v219

Includes release refinements and updates the UI build directory.

- Makefile,internal/server: various release tweaks
- [PR #801](https://github.com/mostlygeek/llama-swap/pull/801) ui-svelte: update build directory

## v218

Introduces a new routing backend and adds a power-draw column for ROCm metrics.

- [PR #790](https://github.com/mostlygeek/llama-swap/pull/790) Introduce new routing backend
- [PR #788](https://github.com/mostlygeek/llama-swap/pull/788) Add new power draw column header for rocm-smi monitoring

## v217

Improves ROCm and Windows GPU monitoring, plus configuration and automation
maintenance.

- [PR #775](https://github.com/mostlygeek/llama-swap/pull/775) Improve rocm-smi performance monitoring
- [PR #773](https://github.com/mostlygeek/llama-swap/pull/773) Added Windows performance monitoring using nvidia-smi
- Disable auto review feature in coderabbit config
- Increase inactivity thresholds for stale issues
- config.example.yaml: Improve matrix vs groups info

## v216

Updates the UI link to the performance discussion thread.

- ui-svelte: update link to performance discussion thread

## v215

Adds ROCm GPU statistics through rocm-smi.

- [PR #767](https://github.com/mostlygeek/llama-swap/pull/767) Add ROCm stats via rocm-smi

## v214

Fixes cached-token totals and updates nvidia-smi polling for newer drivers.

- [PR #760](https://github.com/mostlygeek/llama-swap/pull/760) ui-svelte: fix cached tokens total counting -1 sentinel
- [PR #759](https://github.com/mostlygeek/llama-swap/pull/759) fix: use --loop instead of -loop for nvidia-smi

## v213

Updates UI packages and improves compatibility with v1/messages and
v1/responses endpoints.

- ui-svelte: package updates
- [PR #758](https://github.com/mostlygeek/llama-swap/pull/758) proxy,ui-svelte: improve support for v1/messages and v1/responses

## v212

Adds multi-architecture CPU images, Prometheus performance metrics, automatic
themes, and several CI and proxy fixes.

- ci: set go-version-file in release workflow
- ci: fix workflow bugs in release and go-ci
- [PR #750](https://github.com/mostlygeek/llama-swap/pull/750) Changes and fixes before the release (docs/small tweaks)
- [PR #753](https://github.com/mostlygeek/llama-swap/pull/753) perf: ignore LACT devices reporting zero VRAM
- [PR #751](https://github.com/mostlygeek/llama-swap/pull/751) ci: use manifest-aware cleanup action for multi-arch :cpu
- [PR #746](https://github.com/mostlygeek/llama-swap/pull/746) Multi arch cpu
- [PR #748](https://github.com/mostlygeek/llama-swap/pull/748) proxy: fix data race in /running endpoint and typo in error message
- [PR #741](https://github.com/mostlygeek/llama-swap/pull/741) ui: add auto theme switch mode based on system theme
- [PR #743](https://github.com/mostlygeek/llama-swap/pull/743) proxy,ui: add performance monitoring with Prometheus metrics
- [PR #733](https://github.com/mostlygeek/llama-swap/pull/733) proxy: add versionless API endpoint
- [PR #731](https://github.com/mostlygeek/llama-swap/pull/731) llama-swap.go: remove debounce, replace fmt.Printlns

## v211

Fixes process logging when matrix configuration selects processes.

- proxy: fix logger not checking matrix for processes

## v210

Fixes non-streaming response durations and improves no-history and log
monitoring behavior.

- [PR #723](https://github.com/mostlygeek/llama-swap/pull/723) proxy: fix zero duration for non streaming responses
- [PR #721](https://github.com/mostlygeek/llama-swap/pull/721) fix: ?no-history flag and improve /logs monitoring docs

## v209

Refactors the Activity page, removes the macro-length cap, and improves theme
and log-panel controls.

- [PR #710](https://github.com/mostlygeek/llama-swap/pull/710) Refactor Activity Page
- [PR #718](https://github.com/mostlygeek/llama-swap/pull/718) config: remove hard cap on macro length
- [PR #712](https://github.com/mostlygeek/llama-swap/pull/712) ui-svelte: default theme to user preferred color scheme
- ui-svelte: make it easier to toggle panels in logs view

## v208

Adds reasoning-content support and prompt-processing histograms, and fixes
architecture-specific installation downloads.

- [PR #708](https://github.com/mostlygeek/llama-swap/pull/708) ui-svelte: support reasoning and reasoning_content
- [PR #705](https://github.com/mostlygeek/llama-swap/pull/705) ui-svelte: add prompt processing histogram
- [PR #698](https://github.com/mostlygeek/llama-swap/pull/698) fix: support architecture-specific download URLs in install script

## v207

No user-facing changes were included between v206 and v207.

## v206

Fixes UI histogram calculation and publishes unified Docker images on scheduled
runs.

- [PR #695](https://github.com/mostlygeek/llama-swap/pull/695) ui-svelte: fix histogram calculation
- [PR #694](https://github.com/mostlygeek/llama-swap/pull/694) Push unified docker images on scheduled runs

## v205

Replaces the file watcher with stat polling and SIGHUP reloads, and adds uv to
the unified Docker image.

- [PR #685](https://github.com/mostlygeek/llama-swap/pull/685) proxy: replace fsnotify with stat-poll watcher and add SIGHUP reload
- [PR #681](https://github.com/mostlygeek/llama-swap/pull/681) docker/unified: add uv via pip install

## v204

Fixes matrix and process-stop races and improves unified-image build workflows.

- [PR #677](https://github.com/mostlygeek/llama-swap/pull/677) proxy: fix matrix race and process stop bug
- [PR #676](https://github.com/mostlygeek/llama-swap/pull/676) .github/workflows: tweak push ghcr conditional
- [PR #672](https://github.com/mostlygeek/llama-swap/pull/672) .github/workflows: add toggle for pushing unified images to github
- [PR #669](https://github.com/mostlygeek/llama-swap/pull/669) docker/unified: add spirv-headers to fix vulkan build

## v203

Adds zstd capture compression, fixes swap races, and restores Linux arm64 build
targets.

- [PR #668](https://github.com/mostlygeek/llama-swap/pull/668) proxy: compress captures with zstd
- [PR #667](https://github.com/mostlygeek/llama-swap/pull/667) proxy: fix race conditions during swap
- [PR #660](https://github.com/mostlygeek/llama-swap/pull/660) proxy: Refactor tests
- Makefile: restore linux arm64 targets

## v202

Introduces solver-based matrix model swapping, updates matrix documentation,
and fixes UI security issues.

- docs: update configuration.md for matrix
- README.md: update to use matrix instead of groups
- [PR #646](https://github.com/mostlygeek/llama-swap/pull/646) proxy: add swap matrix with solver-based model swapping
- [PR #649](https://github.com/mostlygeek/llama-swap/pull/649) ui-svelte: fix security issues

## v201

Restores proxy timeouts and derives a rootless unified image from the root
container.

- [PR #648](https://github.com/mostlygeek/llama-swap/pull/648) proxy,proxy/config: restore timeouts to pre PR 619
- [PR #644](https://github.com/mostlygeek/llama-swap/pull/644) docker/unified: derive rootless image from root container

## v200

Adds a rootless unified image, configurable HTTP and CUDA settings, preserved
metrics durations, and several Docker and CI improvements.

- [PR #630](https://github.com/mostlygeek/llama-swap/pull/630) docker/unified: publish rootless image variant
- [PR #629](https://github.com/mostlygeek/llama-swap/pull/629) proxy: preserve wall-clock duration in metrics
- [PR #619](https://github.com/mostlygeek/llama-swap/pull/619) proxy: add configurable HTTP timeouts for models and peers
- [PR #627](https://github.com/mostlygeek/llama-swap/pull/627) ci: validate config.example.yaml against config-schema.json
- [PR #625](https://github.com/mostlygeek/llama-swap/pull/625) docker: make CMAKE_CUDA_ARCHITECTURES configurable via build arg
- [PR #620](https://github.com/mostlygeek/llama-swap/pull/620) docker/unified: add ik_llama.cpp to CUDA container
- add /sdapi to list of supported endpoints
- [PR #616](https://github.com/mostlygeek/llama-swap/pull/616) docker/unified: build llama.cpp with static libraries
- [PR #610](https://github.com/mostlygeek/llama-swap/pull/610) ci: fix matrix exclude for scheduled docker workflow
- [PR #606](https://github.com/mostlygeek/llama-swap/pull/606) docker/unified,.github: fix unified build
- [PR #605](https://github.com/mostlygeek/llama-swap/pull/605) build(deps): bump picomatch from 4.0.3 to 4.0.4 in /ui-svelte
