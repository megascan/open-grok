use std::collections::{BTreeSet, HashMap, VecDeque};
use std::path::Path;
use std::sync::Arc;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering};
use std::time::Duration;

use axum::Json;
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use serde_json::{Value, json};

use crate::conversation::{ConversationId, ConversationTracker, ReadConversation};
use crate::conversation_replay::ConversationReplay;
use crate::inference_request::{
    InferenceRequest, InferenceRequestKind as ReplayRequestKind, model_name,
};
use crate::mock_server::{LogEntry, RequestLog};
use crate::scripted::{BodyHold, BoxWait, ScriptedResponse, TerminalWait};

pub use crate::inference_request::InferenceEndpoint;

/// Coarse request kind used to keep auxiliary calls from stealing turn scripts.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
enum InferenceRequestKind {
    Foreground,
    Auxiliary,
}

impl InferenceRequestKind {
    fn classify(headers: &HeaderMap, body: &Value) -> Self {
        if nonempty_header(headers, "x-grok-turn-idx").is_some() {
            return Self::Foreground;
        }
        if nonempty_header(headers, "x-grok-req-id").is_some() {
            return Self::Auxiliary;
        }
        if body
            .get("tools")
            .and_then(Value::as_array)
            .is_some_and(|tools| tools.len() >= 2)
        {
            Self::Foreground
        } else {
            Self::Auxiliary
        }
    }
}

/// Typed match criteria for one named inference response.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct InferenceRequestMatcher {
    endpoint: InferenceEndpoint,
    kind: InferenceRequestKind,
    body_contains: Option<&'static str>,
}

impl InferenceRequestMatcher {
    /// Match a user-facing agent turn on the selected endpoint.
    pub fn foreground(endpoint: InferenceEndpoint) -> Self {
        Self {
            endpoint,
            kind: InferenceRequestKind::Foreground,
            body_contains: None,
        }
    }

    /// Match title, classifier, prompt-suggestion, or other side-channel work.
    pub fn auxiliary(endpoint: InferenceEndpoint) -> Self {
        Self {
            endpoint,
            kind: InferenceRequestKind::Auxiliary,
            body_contains: None,
        }
    }

    pub fn foreground_containing(endpoint: InferenceEndpoint, fragment: &'static str) -> Self {
        Self {
            body_contains: Some(fragment),
            ..Self::foreground(endpoint)
        }
    }

    pub fn auxiliary_containing(endpoint: InferenceEndpoint, fragment: &'static str) -> Self {
        Self {
            body_contains: Some(fragment),
            ..Self::auxiliary(endpoint)
        }
    }

    fn matches(self, request: &ClassifiedInferenceRequest) -> bool {
        self.endpoint == request.endpoint
            && self.kind == request.kind
            && self
                .body_contains
                .is_none_or(|fragment| request.body.contains(fragment))
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ExpectationPhase {
    Pending,
    Received,
    Blocked,
    Satisfied,
}

struct ExpectationControl {
    name: String,
    phase_tx: tokio::sync::watch::Sender<ExpectationPhase>,
    claims_tx: tokio::sync::watch::Sender<usize>,
    release_tx: tokio::sync::watch::Sender<bool>,
}

impl ExpectationControl {
    fn set_phase(&self, phase: ExpectationPhase) {
        self.phase_tx.send_replace(phase);
    }

    fn release(&self) {
        self.release_tx.send_replace(true);
    }

    fn claim(&self) {
        self.claims_tx.send_modify(|claims| *claims += 1);
    }

    async fn wait_for_release(&self) {
        let mut release_rx = self.release_tx.subscribe();
        if *release_rx.borrow_and_update() {
            return;
        }
        release_rx
            .wait_for(|released| *released)
            .await
            .expect("expectation release sender lives with the claimed response");
    }

    async fn wait_claims(&self, target: usize) {
        let mut claims_rx = self.claims_tx.subscribe();
        claims_rx
            .wait_for(|claims| *claims >= target)
            .await
            .expect("expectation claims sender lives with the control");
    }
}

/// Deterministic lifecycle handle for one registered inference expectation.
#[must_use = "expectation handles provide synchronization and satisfaction checks"]
pub struct InferenceExpectation {
    control: Arc<ExpectationControl>,
    phase_rx: tokio::sync::watch::Receiver<ExpectationPhase>,
}

impl InferenceExpectation {
    pub fn name(&self) -> &str {
        &self.control.name
    }

    pub fn is_satisfied(&self) -> bool {
        *self.phase_rx.borrow() == ExpectationPhase::Satisfied
    }

    /// Wait until one request atomically claims this expectation.
    pub async fn wait_received(&mut self) {
        self.wait_for(ExpectationPhase::Received).await;
    }

    /// Observe receipt without dropping the expectation and releasing its hold.
    pub fn received_wait(&self) -> ReceivedWait {
        ReceivedWait {
            rx: self.phase_rx.clone(),
        }
    }

    /// Wait until the response reaches its terminal-event barrier.
    pub async fn wait_blocked(&mut self) {
        self.wait_for(ExpectationPhase::Blocked).await;
    }

    /// Wait until the primary response pipeline crosses its terminal boundary.
    pub async fn wait_satisfied(&mut self) {
        self.wait_for(ExpectationPhase::Satisfied).await;
    }

    /// Wait for `target` matching claims, including overlapping retries.
    pub async fn wait_claims(&self, target: usize) {
        self.control.wait_claims(target).await;
    }

    /// Release this expectation's terminal barrier.
    pub fn release(&self) {
        self.control.release();
    }

    /// Panic with the expectation name and lifecycle state unless satisfied.
    pub fn assert_satisfied(&self) {
        assert!(
            self.is_satisfied(),
            "inference expectation `{}` was not satisfied (state: {:?})",
            self.name(),
            *self.phase_rx.borrow()
        );
    }

    /// Describe the expectation for aggregation in test failure output.
    pub fn diagnostic(&self) -> String {
        format!(
            "inference expectation `{}` (state: {:?})",
            self.name(),
            *self.phase_rx.borrow()
        )
    }

    async fn wait_for(&mut self, target: ExpectationPhase) {
        if self
            .phase_rx
            .wait_for(|phase| Self::phase_reached(*phase, target))
            .await
            .is_err()
        {
            panic!(
                "inference expectation `{}` closed before reaching {target:?} (state: {:?})",
                self.control.name,
                *self.phase_rx.borrow()
            );
        }
    }

    fn phase_reached(current: ExpectationPhase, target: ExpectationPhase) -> bool {
        match target {
            ExpectationPhase::Pending => true,
            ExpectationPhase::Received => current != ExpectationPhase::Pending,
            ExpectationPhase::Blocked => matches!(
                current,
                ExpectationPhase::Blocked | ExpectationPhase::Satisfied
            ),
            ExpectationPhase::Satisfied => current == ExpectationPhase::Satisfied,
        }
    }
}

pub struct ReceivedWait {
    rx: tokio::sync::watch::Receiver<ExpectationPhase>,
}

impl ReceivedWait {
    pub async fn wait(mut self) {
        let _ = self
            .rx
            .wait_for(|phase| *phase != ExpectationPhase::Pending)
            .await;
    }
}

impl Drop for InferenceExpectation {
    fn drop(&mut self) {
        self.control.release();
    }
}

struct PendingExpectation {
    matcher: InferenceRequestMatcher,
    response: ScriptedResponse,
    block_before_terminal: bool,
    control: Arc<ExpectationControl>,
}

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct ModelCallFingerprint {
    endpoint: InferenceEndpoint,
    kind: InferenceRequestKind,
    request_id: String,
    body: String,
}

struct CallState {
    response: ScriptedResponse,
    block_before_terminal: bool,
    control: Arc<ExpectationControl>,
    active: usize,
    primary_crossed_terminal: bool,
}

#[derive(Default)]
struct ExpectationState {
    pending: VecDeque<PendingExpectation>,
    in_flight: HashMap<ModelCallFingerprint, CallState>,
}

type Expectations = Arc<std::sync::Mutex<ExpectationState>>;
type ScriptQueues = Arc<std::sync::Mutex<HashMap<String, VecDeque<ScriptedResponse>>>>;

#[derive(Clone)]
struct ConcurrencyCap {
    slots: Arc<tokio::sync::Semaphore>,
    hold: Duration,
    retry_after_secs: u64,
}

#[derive(Default)]
struct FallbackHolds {
    foreground: bool,
    auxiliary: bool,
    request_prefix: Option<String>,
}

#[derive(Clone)]
pub(crate) struct InferenceOverrides {
    expectations: Expectations,
    scripted: ScriptQueues,
    completion_gate: Arc<CompletionGate>,
    conversations: ConversationTracker,
    conversation_scripts: ConversationReplay,
    chunk_release: Arc<ChunkReleaseGate>,
    parked_replies: tokio::sync::watch::Sender<bool>,
    scripted_parks: Arc<Mutex<HashMap<u64, Arc<ScriptedPark>>>>,
    next_park_token: Arc<AtomicU64>,
    auxiliary_replies: tokio::sync::watch::Sender<bool>,
    parked_count: Arc<AtomicUsize>,
    parked_by_conversation: Arc<Mutex<HashMap<usize, usize>>>,
    matching_parked: Arc<AtomicUsize>,
    parked_arrived: Arc<tokio::sync::Notify>,
    fallback_holds: Arc<Mutex<FallbackHolds>>,
    required_token: Option<Arc<str>>,
    concurrency_cap: Arc<std::sync::Mutex<Option<ConcurrencyCap>>>,
}

impl InferenceOverrides {
    pub(crate) fn new(required_token: Option<String>) -> Self {
        Self {
            expectations: Arc::new(std::sync::Mutex::new(ExpectationState::default())),
            scripted: Arc::new(std::sync::Mutex::new(HashMap::new())),
            completion_gate: Arc::new(CompletionGate::default()),
            conversations: ConversationTracker::default(),
            conversation_scripts: ConversationReplay::default(),
            chunk_release: Arc::new(ChunkReleaseGate::default()),
            parked_replies: tokio::sync::watch::Sender::new(false),
            scripted_parks: Arc::new(Mutex::new(HashMap::new())),
            next_park_token: Arc::new(AtomicU64::new(0)),
            auxiliary_replies: tokio::sync::watch::Sender::new(false),
            parked_count: Arc::new(AtomicUsize::new(0)),
            parked_by_conversation: Arc::new(Mutex::new(HashMap::new())),
            matching_parked: Arc::new(AtomicUsize::new(0)),
            parked_arrived: Arc::new(tokio::sync::Notify::new()),
            fallback_holds: Arc::new(Mutex::new(FallbackHolds::default())),
            required_token: required_token.map(Arc::from),
            concurrency_cap: Arc::new(std::sync::Mutex::new(None)),
        }
    }

    pub(crate) fn set_concurrency_cap(&self, cap: usize, hold: Duration, retry_after_secs: u64) {
        *self.concurrency_cap.lock().unwrap() = Some(ConcurrencyCap {
            slots: Arc::new(tokio::sync::Semaphore::new(cap)),
            hold,
            retry_after_secs,
        });
    }

    pub(crate) fn replay_request<'a>(
        &self,
        endpoint: InferenceEndpoint,
        headers: &'a HeaderMap,
        body: &'a Value,
    ) -> InferenceRequest<'a> {
        InferenceRequest::new(&self.conversations, endpoint, headers, body)
    }

    pub(crate) fn conversations(&self, log: &[LogEntry]) -> Vec<ReadConversation> {
        self.conversations.snapshot(log)
    }

    pub(crate) fn conversation_scripts(&self) -> &ConversationReplay {
        &self.conversation_scripts
    }

    pub(crate) fn set_foreground_hold(&self) {
        self.fallback_holds.lock().unwrap().foreground = true;
    }
    pub(crate) fn set_auxiliary_hold(&self) {
        self.fallback_holds.lock().unwrap().auxiliary = true;
    }
    pub(crate) fn set_auxiliary_hold_matching(&self, prefix: String) {
        self.fallback_holds.lock().unwrap().request_prefix = Some(prefix);
    }

    pub(crate) async fn hold_next_foreground(&self, request: &InferenceRequest<'_>) {
        let hold = request.kind() == ReplayRequestKind::Foreground
            && std::mem::take(&mut self.fallback_holds.lock().unwrap().foreground);
        if hold {
            self.wait_until_park_released(request.conversation().map(ConversationId::number))
                .await;
        }
    }

    pub(crate) async fn hold_next_auxiliary(&self, request: &InferenceRequest<'_>) {
        if request.kind() != ReplayRequestKind::Auxiliary {
            return;
        }
        let (hold, matched) = {
            let mut holds = self.fallback_holds.lock().unwrap();
            let hold = std::mem::take(&mut holds.auxiliary);
            let matched = request.request_id().is_some_and(|id| {
                holds
                    .request_prefix
                    .as_deref()
                    .is_some_and(|prefix| id.starts_with(prefix))
            });
            if matched {
                holds.request_prefix = None;
            }
            (hold, matched)
        };
        if matched {
            self.note_matching_park();
        }
        if hold || matched {
            self.wait_until_park_released(None).await;
        }
    }

    pub(crate) fn classify(
        &self,
        endpoint: InferenceEndpoint,
        headers: &HeaderMap,
        body: &Value,
    ) -> ClassifiedInferenceRequest {
        let kind = InferenceRequestKind::classify(headers, body);
        let body = serde_json::to_string(body).expect("serialize inference request fingerprint");
        let fingerprint =
            nonempty_header(headers, "x-grok-req-id").map(|request_id| ModelCallFingerprint {
                endpoint,
                kind,
                request_id: request_id.to_owned(),
                body: body.clone(),
            });
        ClassifiedInferenceRequest {
            endpoint,
            kind,
            body,
            fingerprint,
        }
    }

    pub(crate) async fn response_override(
        &self,
        request: &ClassifiedInferenceRequest,
        headers: &HeaderMap,
        delay: Option<Duration>,
    ) -> Option<Response> {
        self.response_override_inner(request, headers, delay, None)
            .await
    }

    pub(crate) async fn response_override_replay(
        &self,
        request: &ClassifiedInferenceRequest,
        headers: &HeaderMap,
        delay: Option<Duration>,
        replay: &InferenceRequest<'_>,
        log: &RequestLog,
        sequence: u64,
    ) -> Option<Response> {
        self.response_override_inner(request, headers, delay, Some((replay, log, sequence)))
            .await
    }

    async fn response_override_inner(
        &self,
        request: &ClassifiedInferenceRequest,
        headers: &HeaderMap,
        delay: Option<Duration>,
        replay: Option<(&InferenceRequest<'_>, &RequestLog, u64)>,
    ) -> Option<Response> {
        if let Some((request, _, _)) = replay {
            if let Ok(body) = serde_json::to_string(request.body()) {
                self.conversation_scripts.release_parks(&body);
            }
        }
        if let Some(claimed) = self.claim_expectation(request) {
            let (response, wait) = claimed.into_parts();
            return Some(
                response
                    .into_response_with_hold(delay, Some(wait), Some(self.body_hold()))
                    .await,
            );
        }

        if let Some(response) = self.pop_scripted(request.endpoint.path()) {
            let wait =
                (request.is_foreground() && response.is_sse()).then(|| self.global_terminal_wait());
            return Some(
                response
                    .into_response_with_hold(delay, wait, Some(self.body_hold()))
                    .await,
            );
        }

        if let Some(rejection) = self.auth_rejection(headers) {
            if let Some((_, log, sequence)) = replay {
                log.note_reply(
                    sequence,
                    None,
                    Some(crate::failure::ObservedFailure::Status(
                        rejection.status().as_u16(),
                    )),
                );
            }
            return Some(rejection);
        }

        if let Some((replay, log, sequence)) = replay
            && let Some(served) = self.conversation_scripts.respond(replay)
        {
            log.note_reply(sequence, served.reply_index, served.observed);
            if let Some(needle) = served.park_until {
                self.conversation_scripts.park_until(needle).await;
            }
            if served.park {
                if let Some(token) = served.park_token {
                    self.wait_for_scripted_park(token).await;
                } else {
                    self.wait_until_park_released(
                        replay.conversation().map(ConversationId::number),
                    )
                    .await;
                }
            }
            if let Some(path) = &served.hold_until_lsp_log {
                wait_for_respawned_lsp(path).await;
            }
            if let Some(hold) = served.hold {
                tokio::time::sleep(hold).await;
            }
            if let Some((conversation, request_number)) = served.hold_until {
                let deadline =
                    tokio::time::Instant::now() + crate::scaled(Duration::from_secs(120));
                while log.conversation_requests(conversation) < request_number {
                    assert!(
                        tokio::time::Instant::now() < deadline,
                        "conversation {conversation} request {request_number} did not arrive"
                    );
                    tokio::time::sleep(Duration::from_millis(20)).await;
                }
            }
            let response = served
                .reply
                .into_response(replay.endpoint(), model_name(replay.body()));
            let wait = if response.is_sse() {
                self.completion_gate.wait_if_held(None).await;
                self.replay_terminal_wait(replay)
            } else {
                None
            };
            return Some(
                response
                    .into_response_with_hold(delay, wait, Some(self.body_hold()))
                    .await,
            );
        }

        let cap = self.concurrency_cap.lock().unwrap().clone();
        if let Some(cap) = cap {
            match Arc::clone(&cap.slots).try_acquire_owned() {
                Ok(permit) => {
                    tokio::time::sleep(cap.hold).await;
                    drop(permit);
                }
                Err(_) => {
                    if let Some((_, log, sequence)) = replay {
                        log.note_reply(
                            sequence,
                            None,
                            Some(crate::failure::ObservedFailure::Status(429)),
                        );
                    }
                    let mut reply = ScriptedResponse::text(429, "concurrent request cap exceeded");
                    reply
                        .headers
                        .push(("retry-after".to_string(), cap.retry_after_secs.to_string()));
                    return Some(reply.into_response_paced(delay, None).await);
                }
            }
        }
        None
    }

    pub(crate) fn register_expectation(
        &self,
        name: impl Into<String>,
        matcher: InferenceRequestMatcher,
        response: ScriptedResponse,
        block_before_terminal: bool,
    ) -> InferenceExpectation {
        response.validate();
        let name = name.into();
        let mut expectations = self.expectations.lock().unwrap();
        assert!(
            expectations
                .pending
                .iter()
                .all(|expectation| expectation.control.name != name)
                && expectations
                    .in_flight
                    .values()
                    .all(|expectation| expectation.control.name != name),
            "duplicate inference expectation name `{name}`"
        );
        let (phase_tx, phase_rx) = tokio::sync::watch::channel(ExpectationPhase::Pending);
        let (claims_tx, _claims_rx) = tokio::sync::watch::channel(0);
        let (release_tx, _release_rx) = tokio::sync::watch::channel(!block_before_terminal);
        let control = Arc::new(ExpectationControl {
            name,
            phase_tx,
            claims_tx,
            release_tx,
        });
        expectations.pending.push_back(PendingExpectation {
            matcher,
            response,
            block_before_terminal,
            control: control.clone(),
        });
        InferenceExpectation { control, phase_rx }
    }

    pub(crate) fn enqueue_response(&self, path: impl Into<String>, response: ScriptedResponse) {
        response.validate();
        self.scripted
            .lock()
            .unwrap()
            .entry(path.into())
            .or_default()
            .push_back(response);
    }

    pub(crate) fn pop_scripted(&self, path: &str) -> Option<ScriptedResponse> {
        self.scripted
            .lock()
            .unwrap()
            .get_mut(path)
            .and_then(VecDeque::pop_front)
    }

    pub(crate) fn fallback_terminal_wait(
        &self,
        request: &ClassifiedInferenceRequest,
    ) -> Option<TerminalWait> {
        request.is_foreground().then(|| self.global_terminal_wait())
    }

    pub(crate) fn replay_terminal_wait(
        &self,
        request: &InferenceRequest<'_>,
    ) -> Option<TerminalWait> {
        let conversation = request.conversation()?;
        let gate = self.completion_gate.clone();
        Some(Box::new(move || {
            Box::pin(async move { gate.wait_if_held(Some(conversation)).await })
        }))
    }

    fn body_hold(&self) -> BodyHold {
        let gate = self.completion_gate.clone();
        let chunks = self.chunk_release.clone();
        BodyHold::new(move || {
            let gate = gate.clone();
            let chunks = chunks.clone();
            Box::pin(async move {
                if chunks.armed() {
                    chunks.wait_one().await;
                } else {
                    gate.wait_if_held(None).await;
                }
            })
        })
    }

    pub(crate) fn arm_chunk_release(&self) {
        self.chunk_release.arm();
    }

    pub(crate) fn release_one_chunk(&self) {
        self.chunk_release.release_one();
    }

    pub(crate) fn release_remaining_chunks(&self) {
        self.chunk_release.release_rest();
    }

    pub(crate) fn hold_completions(&self) {
        self.completion_gate.hold();
    }

    pub(crate) fn arm_reply_hold(&self) -> ArmedReplyHold {
        ArmedReplyHold::arm(Arc::clone(&self.completion_gate))
    }

    /// Keep holding `conversation` and let every other conversation stream its terminal event.
    pub(crate) fn hold_only_conversation(&self, conversation: usize) {
        self.completion_gate.hold_only(conversation);
    }

    pub(crate) fn release_completions(&self) {
        self.completion_gate.release();
    }

    pub(crate) fn hold_conversation(&self, conversation: ConversationId) {
        self.completion_gate.hold_conversation(conversation);
    }

    pub(crate) fn release_conversation(&self, conversation: ConversationId) {
        self.completion_gate.release_conversation(conversation);
    }

    /// Lets every parked scripted reply finish. Called when the mock shuts down.
    pub(crate) fn release_parked_replies(&self) {
        self.parked_replies.send_replace(true);
        self.auxiliary_replies.send_replace(true);
    }

    /// A park released only by [`Self::release_scripted_park`], not by the shutdown latch.
    pub(crate) fn arm_scripted_park(&self) -> u64 {
        let token = self.next_park_token.fetch_add(1, Ordering::SeqCst) + 1;
        let (released, _) = tokio::sync::watch::channel(false);
        self.scripted_parks.lock().unwrap().insert(
            token,
            Arc::new(ScriptedPark {
                released,
                parked: AtomicUsize::new(0),
                arrived: tokio::sync::Notify::new(),
            }),
        );
        token
    }

    pub(crate) fn release_scripted_park(&self, token: u64) {
        if let Some(gate) = self.scripted_parks.lock().unwrap().get(&token) {
            gate.released.send_replace(true);
        }
    }

    async fn wait_for_scripted_park(&self, token: u64) {
        let gate = self
            .scripted_parks
            .lock()
            .unwrap()
            .get(&token)
            .cloned()
            .expect("scripted park");
        gate.parked.fetch_add(1, Ordering::SeqCst);
        gate.arrived.notify_waiters();
        let mut own = gate.released.subscribe();
        let mut shutdown = self.parked_replies.subscribe();
        if !*own.borrow() && !*shutdown.borrow() {
            tokio::select! {
                result = own.wait_for(|open| *open) => {
                    let _ = result;
                }
                result = shutdown.wait_for(|open| *open) => {
                    let _ = result;
                }
            }
        }
        gate.parked.fetch_sub(1, Ordering::SeqCst);
        gate.arrived.notify_waiters();
    }

    pub(crate) async fn wait_until_scripted_park(&self, token: u64) {
        let gate = self
            .scripted_parks
            .lock()
            .unwrap()
            .get(&token)
            .cloned()
            .expect("scripted park");
        loop {
            let arrived = gate.arrived.notified();
            if gate.parked.load(Ordering::SeqCst) >= 1 {
                return;
            }
            arrived.await;
        }
    }

    /// Lets parked auxiliary replies finish and leaves foreground holds parked.
    pub(crate) fn release_auxiliary_replies(&self) {
        self.auxiliary_replies.send_replace(true);
    }

    pub(crate) async fn wait_until_park_released(&self, conversation: Option<usize>) {
        use std::sync::atomic::Ordering;
        self.parked_count.fetch_add(1, Ordering::SeqCst);
        if let Some(conversation) = conversation {
            *self
                .parked_by_conversation
                .lock()
                .unwrap()
                .entry(conversation)
                .or_insert(0) += 1;
        }
        self.parked_arrived.notify_waiters();
        let replies = if conversation.is_some() {
            &self.parked_replies
        } else {
            &self.auxiliary_replies
        };
        let mut released = replies.subscribe();
        if !*released.borrow() {
            let _ = released.wait_for(|open| *open).await;
        }
        self.parked_count.fetch_sub(1, Ordering::SeqCst);
        if let Some(conversation) = conversation {
            let mut counts = self.parked_by_conversation.lock().unwrap();
            if let Some(count) = counts.get_mut(&conversation) {
                *count = count.saturating_sub(1);
            }
        }
        self.parked_arrived.notify_waiters();
    }

    /// The request is parked under a request-id prefix hold. Counted before the park await.
    pub(crate) fn note_matching_park(&self) {
        self.matching_parked.fetch_add(1, Ordering::SeqCst);
        self.parked_arrived.notify_waiters();
    }

    pub(crate) async fn wait_until_matching_parked(&self, at_least: usize) {
        loop {
            let arrived = self.parked_arrived.notified();
            if self.matching_parked.load(Ordering::SeqCst) >= at_least {
                return;
            }
            arrived.await;
        }
    }

    pub(crate) async fn wait_until_parked_replies(&self, at_least: usize) {
        use std::sync::atomic::Ordering;
        loop {
            let arrived = self.parked_arrived.notified();
            if self.parked_count.load(Ordering::SeqCst) >= at_least {
                return;
            }
            arrived.await;
        }
    }

    pub(crate) async fn wait_until_parked_replies_for(&self, conversation: usize, at_least: usize) {
        loop {
            let arrived = self.parked_arrived.notified();
            let count = self
                .parked_by_conversation
                .lock()
                .unwrap()
                .get(&conversation)
                .copied()
                .unwrap_or(0);
            if count >= at_least {
                return;
            }
            arrived.await;
        }
    }

    pub(crate) fn agent_completion_parked(&self) -> bool {
        self.completion_gate.parked()
    }

    pub(crate) async fn wait_until_a_reply_is_held(&self) {
        self.completion_gate.wait_until_a_reply_is_held().await;
    }

    fn claim_expectation(
        &self,
        request: &ClassifiedInferenceRequest,
    ) -> Option<ClaimedExpectation> {
        let mut expectations = self.expectations.lock().unwrap();
        if let Some(fingerprint) = request.fingerprint.as_ref()
            && let Some(call) = expectations.in_flight.get_mut(fingerprint)
            && call.active > 0
        {
            call.active += 1;
            call.control.claim();
            return Some(ClaimedExpectation {
                response: call.response.clone(),
                lease: ClaimLease::new(
                    self.expectations.clone(),
                    Some(fingerprint.clone()),
                    call.control.clone(),
                    call.block_before_terminal,
                    ClaimRole::Replay,
                ),
            });
        }

        let index = expectations
            .pending
            .iter()
            .position(|expectation| expectation.matcher.matches(request))?;
        let expectation = expectations
            .pending
            .remove(index)
            .expect("matched expectation index must remain valid");
        expectation.control.set_phase(ExpectationPhase::Received);
        expectation.control.claim();
        let lease = ClaimLease::new(
            self.expectations.clone(),
            request.fingerprint.clone(),
            expectation.control.clone(),
            expectation.block_before_terminal,
            ClaimRole::Primary,
        );
        if let Some(fingerprint) = request.fingerprint.clone() {
            let replaced = expectations.in_flight.insert(
                fingerprint,
                CallState {
                    response: expectation.response.clone(),
                    block_before_terminal: expectation.block_before_terminal,
                    control: expectation.control,
                    active: 1,
                    primary_crossed_terminal: false,
                },
            );
            assert!(
                replaced.is_none(),
                "duplicate in-flight model-call fingerprint"
            );
        }
        Some(ClaimedExpectation {
            response: expectation.response,
            lease,
        })
    }

    pub(crate) fn auth_rejection(&self, headers: &HeaderMap) -> Option<Response> {
        let expected = self.required_token.as_deref()?;
        let valid = headers
            .get("authorization")
            .and_then(|value| value.to_str().ok())
            .is_some_and(|value| {
                value
                    .strip_prefix("Bearer ")
                    .or_else(|| value.strip_prefix("bearer "))
                    .is_some_and(|token| token == expected)
            });
        if valid {
            return None;
        }
        Some(
            (
                StatusCode::UNAUTHORIZED,
                Json(json!({
                    "error": "missing API key; set the x-api-key header or Authorization: Bearer header"
                })),
            )
                .into_response(),
        )
    }

    fn global_terminal_wait(&self) -> TerminalWait {
        let completion_gate = self.completion_gate.clone();
        Box::new(move || Box::pin(async move { completion_gate.wait_if_held(None).await }))
    }
}

pub(crate) struct ClassifiedInferenceRequest {
    endpoint: InferenceEndpoint,
    kind: InferenceRequestKind,
    body: String,
    fingerprint: Option<ModelCallFingerprint>,
}

impl ClassifiedInferenceRequest {
    pub(crate) fn is_foreground(&self) -> bool {
        self.kind == InferenceRequestKind::Foreground
    }
}

#[derive(Clone)]
enum ClaimRole {
    Primary,
    Replay,
}

struct ClaimedExpectation {
    response: ScriptedResponse,
    lease: ClaimLease,
}

impl ClaimedExpectation {
    fn into_parts(self) -> (ScriptedResponse, TerminalWait) {
        let response = self.response;
        let mut lease = self.lease;
        let wait = Box::new(move || {
            Box::pin(async move {
                if lease.block_before_terminal {
                    lease.mark_blocked();
                    lease.control.wait_for_release().await;
                }
                lease.crossed_terminal = true;
                lease.finish();
            }) as BoxWait
        });
        (response, wait)
    }
}

struct ClaimLease {
    expectations: Expectations,
    fingerprint: Option<ModelCallFingerprint>,
    control: Arc<ExpectationControl>,
    block_before_terminal: bool,
    role: ClaimRole,
    crossed_terminal: bool,
    finished: bool,
}

impl ClaimLease {
    fn new(
        expectations: Expectations,
        fingerprint: Option<ModelCallFingerprint>,
        control: Arc<ExpectationControl>,
        block_before_terminal: bool,
        role: ClaimRole,
    ) -> Self {
        Self {
            expectations,
            fingerprint,
            control,
            block_before_terminal,
            role,
            crossed_terminal: false,
            finished: false,
        }
    }

    fn mark_blocked(&self) {
        if matches!(&self.role, ClaimRole::Primary)
            && *self.control.phase_tx.borrow() != ExpectationPhase::Satisfied
        {
            self.control.set_phase(ExpectationPhase::Blocked);
        }
    }

    fn finish(&mut self) {
        if self.finished {
            return;
        }
        self.finished = true;
        self.update_shared_state();
    }

    fn update_shared_state(&self) {
        let Some(fingerprint) = self.fingerprint.as_ref() else {
            if matches!(&self.role, ClaimRole::Primary) && self.crossed_terminal {
                self.control.set_phase(ExpectationPhase::Satisfied);
            }
            return;
        };
        let mut expectations = self.expectations.lock().unwrap();
        let Some(call) = expectations.in_flight.get_mut(fingerprint) else {
            return;
        };
        assert!(call.active > 0, "claim active count underflow");
        call.active -= 1;
        if matches!(&self.role, ClaimRole::Primary) && self.crossed_terminal {
            call.primary_crossed_terminal = true;
        }
        if call.active == 0 {
            let control = call.control.clone();
            let satisfied = call.primary_crossed_terminal;
            expectations.in_flight.remove(fingerprint);
            if satisfied {
                control.set_phase(ExpectationPhase::Satisfied);
            }
        }
    }
}

impl Drop for ClaimLease {
    fn drop(&mut self) {
        self.finish();
    }
}

const LSP_RESPAWN_BOUND: Duration = Duration::from_secs(20);
const LSP_LOG_POLL: Duration = Duration::from_millis(20);

/// Waits until `log` names two language-server processes. The mock appends one line per request,
/// so the second pid is the respawned server's own request, not a timer.
async fn wait_for_respawned_lsp(log: &Path) {
    let deadline = tokio::time::Instant::now() + crate::scaled(LSP_RESPAWN_BOUND);
    loop {
        if respawned_server_opened_a_document(log) {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "the respawned language server did not log a request"
        );
        tokio::time::sleep(LSP_LOG_POLL).await;
    }
}

/// The respawned server logs `textDocument/didOpen` once its documents are replayed, which is the
/// same moment it records its pid. Two such lines means the first server and its replacement.
fn respawned_server_opened_a_document(log: &Path) -> bool {
    let Ok(text) = std::fs::read_to_string(log) else {
        return false;
    };
    text.lines()
        .filter(|line| line.ends_with("textDocument/didOpen"))
        .count()
        >= 2
}

#[derive(Default)]
struct ChunkReleaseGate {
    armed: AtomicBool,
    permits: AtomicUsize,
    notify: tokio::sync::Notify,
}

impl ChunkReleaseGate {
    fn arm(&self) {
        self.permits.store(0, Ordering::SeqCst);
        self.armed.store(true, Ordering::SeqCst);
    }

    fn armed(&self) -> bool {
        self.armed.load(Ordering::SeqCst)
    }

    fn release_one(&self) {
        let mut current = self.permits.load(Ordering::SeqCst);
        loop {
            if current == usize::MAX {
                return;
            }
            match self.permits.compare_exchange(
                current,
                current.saturating_add(1),
                Ordering::SeqCst,
                Ordering::SeqCst,
            ) {
                Ok(_) => {
                    self.notify.notify_waiters();
                    return;
                }
                Err(seen) => current = seen,
            }
        }
    }

    fn release_rest(&self) {
        self.permits.store(usize::MAX, Ordering::SeqCst);
        self.armed.store(false, Ordering::SeqCst);
        self.notify.notify_waiters();
    }

    async fn wait_one(&self) {
        loop {
            let notified = self.notify.notified();
            if !self.armed.load(Ordering::SeqCst) {
                return;
            }
            let current = self.permits.load(Ordering::SeqCst);
            if current == usize::MAX {
                return;
            }
            if current > 0
                && self
                    .permits
                    .compare_exchange(current, current - 1, Ordering::SeqCst, Ordering::SeqCst)
                    .is_ok()
            {
                return;
            }
            notified.await;
        }
    }
}

struct ScriptedPark {
    released: tokio::sync::watch::Sender<bool>,
    parked: AtomicUsize,
    arrived: tokio::sync::Notify,
}

/// The reply hold for one turn. [`release`](Self::release) and drop both disarm it.
pub struct ArmedReplyHold {
    gate: Arc<CompletionGate>,
    armed: AtomicBool,
}

impl ArmedReplyHold {
    fn arm(gate: Arc<CompletionGate>) -> Self {
        gate.hold();
        Self {
            gate,
            armed: AtomicBool::new(true),
        }
    }

    pub fn release(&self) {
        if self.armed.swap(false, Ordering::SeqCst) {
            self.gate.release();
        }
    }
}

impl Drop for ArmedReplyHold {
    fn drop(&mut self) {
        self.release();
    }
}

#[derive(Default)]
struct CompletionGate {
    held: AtomicBool,
    /// When set, only this conversation number waits. Other conversations stream.
    only: Mutex<Option<usize>>,
    held_conversations: std::sync::Mutex<BTreeSet<ConversationId>>,
    parked: AtomicUsize,
    notify: tokio::sync::Notify,
    arrived: tokio::sync::Notify,
}

impl CompletionGate {
    fn hold(&self) {
        self.held.store(true, Ordering::SeqCst);
    }

    fn hold_only(&self, conversation: usize) {
        *self.only.lock().unwrap() = Some(conversation);
    }

    fn release(&self) {
        self.held.store(false, Ordering::SeqCst);
        *self.only.lock().unwrap() = None;
        self.notify.notify_waiters();
    }

    fn hold_conversation(&self, conversation: ConversationId) {
        self.held_conversations.lock().unwrap().insert(conversation);
    }

    fn release_conversation(&self, conversation: ConversationId) {
        self.held_conversations
            .lock()
            .unwrap()
            .remove(&conversation);
        self.notify.notify_waiters();
    }

    fn parked(&self) -> bool {
        self.parked.load(Ordering::SeqCst) > 0
    }

    async fn wait_until_a_reply_is_held(&self) {
        loop {
            let arrived = self.arrived.notified();
            if self.parked.load(Ordering::SeqCst) > 0 {
                return;
            }
            arrived.await;
        }
    }

    async fn wait_if_held(&self, conversation: Option<ConversationId>) {
        loop {
            let notified = self.notify.notified();
            let conversation_held = conversation.is_some_and(|conversation| {
                self.held_conversations
                    .lock()
                    .unwrap()
                    .contains(&conversation)
            });
            if !self.held.load(Ordering::SeqCst) && !conversation_held {
                return;
            }
            let only = *self.only.lock().unwrap();
            if only.is_some_and(|held| conversation.map(ConversationId::number) != Some(held)) {
                return;
            }
            let parked = ParkedGuard::arm(&self.parked);
            self.arrived.notify_waiters();
            notified.await;
            drop(parked);
        }
    }
}

struct ParkedGuard<'a>(&'a AtomicUsize);

impl<'a> ParkedGuard<'a> {
    fn arm(parked: &'a AtomicUsize) -> Self {
        parked.fetch_add(1, Ordering::SeqCst);
        Self(parked)
    }
}

impl Drop for ParkedGuard<'_> {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::SeqCst);
    }
}

fn nonempty_header<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .filter(|value| !value.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn matching_body_and_receipt_observer_do_not_release_the_hold() {
        let overrides = InferenceOverrides::new(None);
        let matcher = InferenceRequestMatcher::auxiliary_containing(
            InferenceEndpoint::Responses,
            "summary prompt",
        );
        let mut expectation = overrides.register_expectation(
            "summary",
            matcher,
            ScriptedResponse::text(200, "ok"),
            true,
        );
        let headers = HeaderMap::new();
        let other = overrides.classify(
            InferenceEndpoint::Responses,
            &headers,
            &json!({"input":"title"}),
        );
        assert!(overrides.claim_expectation(&other).is_none());
        let wait = expectation.received_wait();
        let request = overrides.classify(
            InferenceEndpoint::Responses,
            &headers,
            &json!({"input":"summary prompt"}),
        );
        let claimed = overrides
            .claim_expectation(&request)
            .expect("matching request claims expectation");
        wait.wait().await;
        assert!(!*expectation.control.release_tx.borrow());
        expectation.release();
        drop(claimed);
        expectation.wait_received().await;
    }

    #[tokio::test]
    async fn concurrency_cap_releases_slots_after_hold_and_cancellation() {
        let overrides = InferenceOverrides::new(None);
        overrides.set_concurrency_cap(1, Duration::from_millis(10), 7);
        let headers = HeaderMap::new();
        let request = overrides.classify(InferenceEndpoint::ChatCompletions, &headers, &json!({}));
        let mut admitted = Box::pin(overrides.response_override(&request, &headers, None));
        assert!(futures_util::poll!(&mut admitted).is_pending());

        let rejected = overrides
            .response_override(&request, &headers, None)
            .await
            .expect("the occupied slot must reject another default response");
        assert_eq!(rejected.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(rejected.headers()["retry-after"], "7");
        assert!(admitted.await.is_none());

        let mut cancelled = Box::pin(overrides.response_override(&request, &headers, None));
        assert!(futures_util::poll!(&mut cancelled).is_pending());
        drop(cancelled);

        let mut replacement = Box::pin(overrides.response_override(&request, &headers, None));
        assert!(futures_util::poll!(&mut replacement).is_pending());
        assert!(replacement.await.is_none());
    }
}
