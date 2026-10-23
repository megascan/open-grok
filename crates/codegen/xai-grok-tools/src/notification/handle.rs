use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use super::types::{
    BashExecutionBackgrounded, BashExecutionComplete, BashExecutionFailed, BashExecutionTimeout,
    BashOutputChunk, FileWritten, LspServerCrashed, LspServerFailed, LspServerReady,
    LspServerRetrying, LspServerStarting, MonitorEvent, PlanModeEntered, PlanModeExited,
    ScheduledTaskCreated, ScheduledTaskFired, ScheduledTaskRemoved, SubagentCompleted,
    ToolNotification, UserQuestionAsked,
};
use crate::types::TaskSnapshot;

/// Envelope for consumers that can acknowledge durable notification handling.
pub struct AcknowledgedToolNotification {
    /// Notification delivered in the same FIFO as unacknowledged events.
    pub notification: ToolNotification,
    /// Completion sender present only when the producer requested acknowledgement.
    pub acknowledgement: Option<tokio::sync::oneshot::Sender<Result<(), String>>>,
}

/// Private bridge control messages, ordered with ordinary tool notifications.
pub enum BridgeToolNotification {
    Notification(
        AcknowledgedToolNotification,
        Option<tokio::sync::oneshot::Sender<Result<(), String>>>,
        bool,
    ),
    Barrier(tokio::sync::oneshot::Sender<Result<(), String>>),
    ExternalStart(tokio::sync::oneshot::Receiver<Result<(), String>>),
    SubagentAdmitted {
        id: String,
        generation: String,
        acknowledgement: tokio::sync::oneshot::Sender<Result<(), String>>,
    },
    SubagentAdmissionEnded {
        id: String,
        generation: String,
        error: Option<String>,
    },
}

/// Immutable receipt collection for exactly one turn. It is captured by each
/// tool invocation before it can await or detach work.
#[derive(Clone, Debug)]
pub struct BackgroundDeliveryScope {
    id: String,
    headless: bool,
    state: Arc<parking_lot::Mutex<BackgroundDeliveryScopeState>>,
    revoked: Arc<AtomicBool>,
    session_revoked: Arc<AtomicBool>,
}

#[derive(Debug, Default)]
struct BackgroundDeliveryScopeState {
    closed: bool,
    receipts: Vec<tokio::sync::oneshot::Receiver<Result<(), String>>>,
    announced_tasks: HashSet<String>,
}

impl Default for BackgroundDeliveryScope {
    fn default() -> Self {
        Self::new()
    }
}

impl BackgroundDeliveryScope {
    pub fn new() -> Self {
        Self::for_mode(false, Arc::new(AtomicBool::new(false)))
    }

    fn for_mode(headless: bool, session_revoked: Arc<AtomicBool>) -> Self {
        Self {
            id: uuid::Uuid::now_v7().to_string(),
            headless,
            state: Arc::new(parking_lot::Mutex::new(
                BackgroundDeliveryScopeState::default(),
            )),
            revoked: Arc::new(AtomicBool::new(false)),
            session_revoked,
        }
    }

    /// An independent receipt collection with the same turn cancellation owner.
    pub fn child(&self) -> Self {
        Self {
            id: uuid::Uuid::now_v7().to_string(),
            headless: self.headless,
            state: Arc::new(parking_lot::Mutex::new(
                BackgroundDeliveryScopeState::default(),
            )),
            revoked: self.revoked.clone(),
            session_revoked: self.session_revoked.clone(),
        }
    }

    /// Revoke pending starts for this exact turn before its stop sweep.
    pub fn revoke(&self) {
        self.revoked.store(true, Ordering::Release);
    }

    pub fn is_revoked(&self) -> bool {
        self.revoked.load(Ordering::Acquire) || self.session_revoked.load(Ordering::Acquire)
    }

    pub fn is_session_revoked(&self) -> bool {
        self.session_revoked.load(Ordering::Acquire)
    }

    pub fn is_headless(&self) -> bool {
        self.headless
    }

    fn register(
        &self,
        receipt: tokio::sync::oneshot::Receiver<Result<(), String>>,
    ) -> Result<(), String> {
        let mut state = self.state.lock();
        if state.closed || self.is_revoked() {
            return Err("originating turn no longer accepts background starts".into());
        }
        state.receipts.push(receipt);
        Ok(())
    }

    /// Seal this turn against later registrations and await only its receipts.
    pub async fn wait(&self) -> Result<(), NotificationAcknowledgementError> {
        let receipts = {
            let mut state = self.state.lock();
            state.closed = true;
            std::mem::take(&mut state.receipts)
        };
        NotificationAcknowledgementBatch {
            receipts,
            dispatch_closed: 0,
        }
        .wait()
        .await
    }

    /// A client which stops acknowledging must not wedge an ACP session forever.
    pub async fn wait_bounded(&self, timeout: std::time::Duration) -> Result<(), String> {
        tokio::time::timeout(timeout, self.wait())
            .await
            .map_err(|_| "background lifecycle delivery timed out".to_owned())?
            .map_err(|error| error.to_string())
    }

    fn close(&self) {
        let mut state = self.state.lock();
        state.closed = true;
        state.receipts.clear();
    }
}

/// Clearing a cancelled owner cannot clear the active scope of a newer turn.
pub struct BackgroundDeliveryGuard {
    scope: BackgroundDeliveryScope,
    targets: Arc<[ToolNotificationTarget]>,
}

impl BackgroundDeliveryGuard {
    pub fn scope(&self) -> BackgroundDeliveryScope {
        self.scope.clone()
    }
}

impl Drop for BackgroundDeliveryGuard {
    fn drop(&mut self) {
        self.scope.close();
        for target in self.targets.iter() {
            if let ToolNotificationTarget::Bridge(target) = target {
                let mut active = target.active_scope.lock();
                if active
                    .as_ref()
                    .is_some_and(|scope| scope.id == self.scope.id)
                {
                    *active = None;
                }
            }
        }
    }
}

/// A start receipt transferred with an asynchronous launch command.
#[derive(Debug)]
pub struct BackgroundStartReservation {
    acknowledgements: Vec<tokio::sync::oneshot::Sender<Result<(), String>>>,
    started: bool,
}

impl BackgroundStartReservation {
    /// Once work may exist, losing the delivery task is a failure, not a rejection.
    pub fn mark_started(&mut self) {
        self.started = true;
    }

    pub fn resolve(mut self, result: Result<(), String>) {
        for acknowledgement in self.acknowledgements.drain(..) {
            let _ = acknowledgement.send(result.clone());
        }
    }
}

impl Drop for BackgroundStartReservation {
    fn drop(&mut self) {
        let result = if self.started {
            Err("background start delivery was abandoned after launch".to_owned())
        } else {
            Ok(())
        };
        for acknowledgement in self.acknowledgements.drain(..) {
            let _ = acknowledgement.send(result.clone());
        }
    }
}

/// One unique admission. Ownership moves with the coordinator command, so
/// cancellation of a caller cannot settle a later request with the same id.
pub struct SubagentAdmissionReservation {
    generation: String,
    enqueued: bool,
    targets: Vec<(
        tokio::sync::mpsc::UnboundedSender<BridgeToolNotification>,
        tokio::sync::oneshot::Sender<Result<(), String>>,
    )>,
}

impl std::fmt::Debug for SubagentAdmissionReservation {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SubagentAdmissionReservation")
            .field("generation", &self.generation)
            .finish_non_exhaustive()
    }
}

/// Coordinator-owned backstop for admitted work if the coordinator drops.
pub struct AdmittedSubagent {
    id: String,
    generation: String,
    targets: Vec<tokio::sync::mpsc::UnboundedSender<BridgeToolNotification>>,
    delivery_error: parking_lot::Mutex<Option<String>>,
}

impl AdmittedSubagent {
    pub fn generation(&self) -> &str {
        &self.generation
    }

    /// Carry an uncertain member delivery on the cohort's terminal frame.
    pub fn report_delivery_failure(&self, error: &str) {
        self.delivery_error
            .lock()
            .get_or_insert_with(|| error.to_owned());
    }

    /// The ordinary completion path will send the terminal event itself.
    pub fn completed(mut self) {
        self.targets.clear();
    }
}

impl Drop for AdmittedSubagent {
    fn drop(&mut self) {
        for target in &self.targets {
            let _ = target.send(BridgeToolNotification::SubagentAdmissionEnded {
                id: self.id.clone(),
                generation: self.generation.clone(),
                error: self.delivery_error.lock().clone(),
            });
        }
    }
}

impl SubagentAdmissionReservation {
    pub fn mark_enqueued(&mut self) {
        self.enqueued = true;
    }

    /// An expected admission rejection has no client lifecycle to deliver.
    pub fn reject(mut self) {
        self.enqueued = false;
    }

    /// Publish admission before a queued or directly-started request can
    /// return. The bridge acknowledges only after client delivery.
    pub fn admit(mut self, id: &str) -> AdmittedSubagent {
        let mut targets = Vec::new();
        for (target, acknowledgement) in self.targets.drain(..) {
            let message = BridgeToolNotification::SubagentAdmitted {
                id: id.to_owned(),
                generation: self.generation.clone(),
                acknowledgement,
            };
            match target.send(message) {
                Ok(()) => targets.push(target),
                Err(error) => {
                    if let BridgeToolNotification::SubagentAdmitted {
                        acknowledgement, ..
                    } = error.0
                    {
                        let _ = acknowledgement
                            .send(Err("background notification bridge closed".into()));
                    }
                }
            }
        }
        AdmittedSubagent {
            id: id.to_owned(),
            generation: self.generation.clone(),
            targets,
            delivery_error: parking_lot::Mutex::new(None),
        }
    }
}

impl Drop for SubagentAdmissionReservation {
    fn drop(&mut self) {
        // An unexpected loss after enqueue must fail closed; an ordinary
        // rejection or pre-enqueue cancellation has no lifecycle to deliver.
        for (_, acknowledgement) in self.targets.drain(..) {
            let result = if self.enqueued {
                Err("subagent coordinator dropped an enqueued admission".into())
            } else {
                Ok(())
            };
            let _ = acknowledgement.send(result);
        }
    }
}

#[derive(Clone)]
struct BridgeTarget {
    sender: tokio::sync::mpsc::UnboundedSender<BridgeToolNotification>,
    starts:
        Arc<parking_lot::Mutex<HashMap<String, tokio::sync::oneshot::Sender<Result<(), String>>>>>,
    active_scope: Arc<parking_lot::Mutex<Option<BackgroundDeliveryScope>>>,
    session_revoked: Arc<AtomicBool>,
}

/// Failure reported after all acknowledged notification targets have settled.
#[derive(Debug, PartialEq, Eq, thiserror::Error)]
pub enum NotificationAcknowledgementError {
    #[error("{0} acknowledging notification target(s) closed during dispatch")]
    DispatchClosed(usize),
    #[error("{0} notification acknowledgement(s) were dropped")]
    AcknowledgementDropped(usize),
    #[error("notification consumer rejected delivery: {0:?}")]
    ConsumerRejected(Vec<String>),
    #[error(
        "notification acknowledgement failed: {dispatch_closed} dispatch closed, {acknowledgements_dropped} acknowledgement(s) dropped, consumer rejections: {consumer_rejections:?}"
    )]
    Multiple {
        dispatch_closed: usize,
        acknowledgements_dropped: usize,
        consumer_rejections: Vec<String>,
    },
}

/// Receipts for one acknowledged fan-out operation.
#[must_use = "acknowledged notification receipts must be awaited"]
pub struct NotificationAcknowledgementBatch {
    receipts: Vec<tokio::sync::oneshot::Receiver<Result<(), String>>>,
    dispatch_closed: usize,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DurableNotificationTargets {
    None,
    Present,
}

impl NotificationAcknowledgementBatch {
    /// Wait for every live durable target and report all observed failure classes.
    pub async fn wait(self) -> Result<(), NotificationAcknowledgementError> {
        let mut acknowledgements_dropped = 0;
        let mut consumer_rejections = Vec::new();
        for receipt in self.receipts {
            match receipt.await {
                Ok(Ok(())) => {}
                Ok(Err(error)) => consumer_rejections.push(error),
                Err(_) => acknowledgements_dropped += 1,
            }
        }
        match (
            self.dispatch_closed,
            acknowledgements_dropped,
            consumer_rejections.is_empty(),
        ) {
            (0, 0, true) => Ok(()),
            (dispatch_closed, 0, true) => Err(NotificationAcknowledgementError::DispatchClosed(
                dispatch_closed,
            )),
            (0, acknowledgements_dropped, true) => Err(
                NotificationAcknowledgementError::AcknowledgementDropped(acknowledgements_dropped),
            ),
            (0, 0, false) => Err(NotificationAcknowledgementError::ConsumerRejected(
                consumer_rejections,
            )),
            (dispatch_closed, acknowledgements_dropped, _) => {
                Err(NotificationAcknowledgementError::Multiple {
                    dispatch_closed,
                    acknowledgements_dropped,
                    consumer_rejections,
                })
            }
        }
    }
}

#[derive(Clone)]
enum ToolNotificationTarget {
    Plain(tokio::sync::mpsc::UnboundedSender<ToolNotification>),
    Bounded(tokio::sync::mpsc::Sender<ToolNotification>),
    Capped(Arc<CappedNotificationQueue>),
    Acknowledged(tokio::sync::mpsc::UnboundedSender<AcknowledgedToolNotification>),
    Bridge(BridgeTarget),
}

struct CappedNotificationQueue {
    queue: parking_lot::Mutex<VecDeque<ToolNotification>>,
    capacity: usize,
    closed: AtomicBool,
    ready: tokio::sync::Notify,
}

impl CappedNotificationQueue {
    fn push(&self, notification: ToolNotification) {
        if self.closed.load(Ordering::Relaxed) {
            return;
        }
        let mut queue = self.queue.lock();
        if queue.len() >= self.capacity {
            if !is_critical_notification(&notification) {
                tracing::warn!("tool notification queue full; dropping newest lossy event");
                return;
            }
            let evict = queue
                .iter()
                .position(|queued| !is_critical_notification(queued))
                .unwrap_or(0);
            queue.remove(evict);
            tracing::warn!("tool notification queue full; evicting older event for terminal event");
        }
        queue.push_back(notification);
        drop(queue);
        self.ready.notify_one();
    }
}

fn is_critical_notification(notification: &ToolNotification) -> bool {
    matches!(
        notification,
        ToolNotification::BashExecutionComplete(_)
            | ToolNotification::BashExecutionTimeout(_)
            | ToolNotification::BashExecutionFailed(_)
            | ToolNotification::TaskCompleted(_)
            | ToolNotification::SubagentCompleted(_)
            | ToolNotification::PlanModeEntered(_)
            | ToolNotification::PlanModeExited(_)
            | ToolNotification::UserQuestionAsked(_)
            | ToolNotification::AsyncUserMessage(_)
            | ToolNotification::LspServerCrashed(_)
            | ToolNotification::LspServerFailed(_)
            | ToolNotification::ScheduledTaskFired(_)
            | ToolNotification::ScheduledTaskRemoved(_)
    )
}

/// Receiver for a capped queue that preserves terminal notifications.
pub struct CappedToolNotificationReceiver {
    queue: Arc<CappedNotificationQueue>,
}

impl CappedToolNotificationReceiver {
    pub async fn recv(&mut self) -> Option<ToolNotification> {
        loop {
            let ready = self.queue.ready.notified();
            if let Some(notification) = self.queue.queue.lock().pop_front() {
                return Some(notification);
            }
            if self.queue.closed.load(Ordering::Relaxed) {
                return None;
            }
            ready.await;
        }
    }
}

impl Drop for CappedToolNotificationReceiver {
    fn drop(&mut self) {
        self.queue.closed.store(true, Ordering::Relaxed);
        self.queue.ready.notify_waiters();
    }
}

/// Cloneable notification fan-out with per-target FIFO ordering.
#[derive(Clone)]
pub struct ToolNotificationHandle {
    targets: Arc<[ToolNotificationTarget]>,
    scope: Option<BackgroundDeliveryScope>,
}

impl Default for ToolNotificationHandle {
    fn default() -> Self {
        Self::noop()
    }
}

macro_rules! convenience_sends {
    ($($method:ident, $ty:ty, $variant:ident);+ $(;)?) => {
        $(pub fn $method(&self, value: $ty) { self.send(ToolNotification::$variant(value)); })+
    };
}

impl ToolNotificationHandle {
    pub fn new(sender: tokio::sync::mpsc::UnboundedSender<ToolNotification>) -> Self {
        Self {
            targets: Arc::from([ToolNotificationTarget::Plain(sender)]),
            scope: None,
        }
    }

    pub fn from_sender(sender: tokio::sync::mpsc::UnboundedSender<ToolNotification>) -> Self {
        Self::new(sender)
    }

    pub fn channel() -> (Self, tokio::sync::mpsc::UnboundedReceiver<ToolNotification>) {
        let (sender, receiver) = tokio::sync::mpsc::unbounded_channel();
        (Self::new(sender), receiver)
    }

    /// Create a capped target that drops the newest event when full.
    pub fn bounded_channel(
        capacity: usize,
    ) -> (Self, tokio::sync::mpsc::Receiver<ToolNotification>) {
        let (sender, receiver) = tokio::sync::mpsc::channel(capacity);
        (
            Self {
                targets: Arc::from([ToolNotificationTarget::Bounded(sender)]),
                scope: None,
            },
            receiver,
        )
    }

    /// Create a capped queue that evicts lossy events before terminal events.
    pub fn capped_channel(capacity: usize) -> (Self, CappedToolNotificationReceiver) {
        let queue = Arc::new(CappedNotificationQueue {
            queue: parking_lot::Mutex::new(VecDeque::new()),
            capacity: capacity.max(1),
            closed: AtomicBool::new(false),
            ready: tokio::sync::Notify::new(),
        });
        (
            Self {
                targets: Arc::from([ToolNotificationTarget::Capped(Arc::clone(&queue))]),
                scope: None,
            },
            CappedToolNotificationReceiver { queue },
        )
    }

    pub fn acknowledged_channel() -> (
        Self,
        tokio::sync::mpsc::UnboundedReceiver<AcknowledgedToolNotification>,
    ) {
        let (sender, receiver) = tokio::sync::mpsc::unbounded_channel();
        (
            Self {
                targets: Arc::from([ToolNotificationTarget::Acknowledged(sender)]),
                scope: None,
            },
            receiver,
        )
    }

    /// Create an ordered bridge queue that also accepts delivery barriers.
    pub fn bridge_channel() -> (
        Self,
        tokio::sync::mpsc::UnboundedReceiver<BridgeToolNotification>,
    ) {
        let (sender, receiver) = tokio::sync::mpsc::unbounded_channel();
        (
            Self {
                targets: Arc::from([ToolNotificationTarget::Bridge(BridgeTarget {
                    sender,
                    starts: Arc::new(parking_lot::Mutex::new(HashMap::new())),
                    active_scope: Arc::new(parking_lot::Mutex::new(None)),
                    session_revoked: Arc::new(AtomicBool::new(false)),
                })]),
                scope: None,
            },
            receiver,
        )
    }

    /// Begin one turn; the guard invalidates it on completion or cancellation.
    pub fn begin_turn_delivery(&self) -> BackgroundDeliveryGuard {
        self.begin_turn_delivery_for_mode(false)
    }

    pub fn begin_turn_delivery_for_mode(&self, headless: bool) -> BackgroundDeliveryGuard {
        let session_revoked = self
            .targets
            .iter()
            .find_map(|target| match target {
                ToolNotificationTarget::Bridge(target) => Some(target.session_revoked.clone()),
                _ => None,
            })
            .unwrap_or_else(|| Arc::new(AtomicBool::new(false)));
        let scope = BackgroundDeliveryScope::for_mode(headless, session_revoked);
        for target in self.targets.iter() {
            if let ToolNotificationTarget::Bridge(target) = target {
                *target.active_scope.lock() = Some(scope.clone());
            }
        }
        BackgroundDeliveryGuard {
            scope,
            targets: self.targets.clone(),
        }
    }

    /// Snapshot synchronously before any per-call awaits.
    pub fn current_delivery_scope(&self) -> Option<BackgroundDeliveryScope> {
        self.targets.iter().find_map(|target| match target {
            ToolNotificationTarget::Bridge(target) => target.active_scope.lock().clone(),
            _ => None,
        })
    }

    /// Bind an invocation to its captured turn, independent of later turns.
    pub fn with_delivery_scope(&self, scope: Option<BackgroundDeliveryScope>) -> Self {
        Self {
            targets: self.targets.clone(),
            scope,
        }
    }

    /// Preserve the originating turn's cancellation when isolating receipts.
    pub fn child_delivery_scope(&self) -> BackgroundDeliveryScope {
        self.scope
            .as_ref()
            .map_or_else(BackgroundDeliveryScope::new, BackgroundDeliveryScope::child)
    }

    pub fn is_delivery_revoked(&self) -> bool {
        self.scope
            .as_ref()
            .is_some_and(BackgroundDeliveryScope::is_revoked)
    }

    /// Destructive teardown revokes deferred starts across this session instance.
    /// A resumed session receives a new bridge and therefore a fresh token.
    pub fn revoke_session_deliveries(&self) {
        for target in self.targets.iter() {
            if let ToolNotificationTarget::Bridge(target) = target {
                target.session_revoked.store(true, Ordering::Release);
            }
        }
    }

    /// Wait until each bridge has delivered prior lifecycle notifications.
    /// Call from the turn task, not from the actor that admits wake commands.
    pub async fn wait_for_bridge(&self) -> Result<(), NotificationAcknowledgementError> {
        let mut batch = NotificationAcknowledgementBatch {
            receipts: Vec::new(),
            dispatch_closed: 0,
        };
        for target in self.targets.iter() {
            if let ToolNotificationTarget::Bridge(target) = target {
                let (ack, receipt) = tokio::sync::oneshot::channel();
                if target
                    .sender
                    .send(BridgeToolNotification::Barrier(ack))
                    .is_ok()
                {
                    batch.receipts.push(receipt);
                } else {
                    batch.dispatch_closed += 1;
                }
            }
        }
        batch.wait().await
    }

    /// Reserve a delivery before an asynchronously consumed launch command.
    pub fn reserve_delivery(&self) -> Result<BackgroundStartReservation, String> {
        let mut reservation = BackgroundStartReservation {
            acknowledgements: Vec::new(),
            started: false,
        };
        for target in self.targets.iter() {
            if let ToolNotificationTarget::Bridge(target) = target {
                if target.sender.is_closed() {
                    reservation.resolve(Err("background notification bridge closed".into()));
                    return Err("background notification bridge closed".into());
                }
                let (ack, receipt) = tokio::sync::oneshot::channel();
                let registered = if let Some(scope) = &self.scope {
                    scope.register(receipt)
                } else {
                    target
                        .sender
                        .send(BridgeToolNotification::ExternalStart(receipt))
                        .map_err(|_| "background notification bridge closed".to_owned())
                };
                if let Err(error) = registered {
                    reservation.resolve(Err(error.clone()));
                    return Err(error);
                }
                reservation.acknowledgements.push(ack);
            }
        }
        Ok(reservation)
    }

    /// Reserve a unique admission before sending a spawn command.
    pub fn reserve_subagent_admission(&self) -> Result<SubagentAdmissionReservation, String> {
        let mut reservation = SubagentAdmissionReservation {
            generation: uuid::Uuid::now_v7().to_string(),
            enqueued: false,
            targets: Vec::new(),
        };
        for target in self.targets.iter() {
            if let ToolNotificationTarget::Bridge(target) = target {
                let (ack, receipt) = tokio::sync::oneshot::channel();
                let registered = if let Some(scope) = &self.scope {
                    scope.register(receipt)
                } else {
                    target
                        .sender
                        .send(BridgeToolNotification::ExternalStart(receipt))
                        .map_err(|_| "background notification bridge closed".to_owned())
                };
                if let Err(error) = registered {
                    for (_, prior) in reservation.targets.drain(..) {
                        let _ = prior.send(Err(error.clone()));
                    }
                    return Err(error);
                }
                reservation.targets.push((target.sender.clone(), ack));
            }
        }
        Ok(reservation)
    }

    /// Reserve an asynchronous start before its tool returns. The producer
    /// must resolve it after the start reaches the client, or on failure.
    pub fn reserve_background_start(&self, id: &str) -> Result<(), String> {
        let mut accepted: Vec<BridgeTarget> = Vec::new();
        for target in self.targets.iter() {
            if let ToolNotificationTarget::Bridge(target) = target {
                let (sender, receiver) = tokio::sync::oneshot::channel();
                let mut starts = target.starts.lock();
                let error = if starts.contains_key(id) {
                    Some(format!("duplicate background start: {id}"))
                } else {
                    starts.insert(id.to_owned(), sender);
                    if target
                        .sender
                        .send(BridgeToolNotification::ExternalStart(receiver))
                        .is_err()
                    {
                        starts.remove(id);
                        Some("background notification bridge closed".into())
                    } else {
                        None
                    }
                };
                drop(starts);
                if let Some(error) = error {
                    for prior in accepted {
                        if let Some(sender) = prior.starts.lock().remove(id) {
                            let _ = sender.send(Err(error.clone()));
                        }
                    }
                    return Err(error);
                }
                accepted.push(target.clone());
            }
        }
        Ok(())
    }

    /// Resolve the matching start reservation; duplicate resolutions are harmless.
    pub fn resolve_background_start(&self, id: &str, result: Result<(), String>) {
        for target in self.targets.iter() {
            if let ToolNotificationTarget::Bridge(target) = target
                && let Some(sender) = target.starts.lock().remove(id)
            {
                let _ = sender.send(result.clone());
            }
        }
    }

    pub fn noop() -> Self {
        let (sender, _receiver) = tokio::sync::mpsc::unbounded_channel();
        Self::new(sender)
    }

    /// Combine handles while preserving each target's send order.
    pub fn tee(handles: Vec<ToolNotificationHandle>) -> ToolNotificationHandle {
        let targets = handles
            .iter()
            .flat_map(|handle| handle.targets.iter().cloned())
            .collect::<Vec<_>>();
        let scope = handles.iter().find_map(|handle| handle.scope.clone());
        Self {
            targets: Arc::from(targets),
            scope,
        }
    }

    pub(crate) fn durable_targets(&self) -> DurableNotificationTargets {
        if self.targets.iter().any(|target| {
            matches!(target, ToolNotificationTarget::Acknowledged(sender) if !sender.is_closed())
                || matches!(target, ToolNotificationTarget::Bridge(target) if !target.sender.is_closed())
        }) {
            DurableNotificationTargets::Present
        } else {
            DurableNotificationTargets::None
        }
    }

    pub fn send(&self, notification: ToolNotification) {
        // A foreground handoff may be announced both by the terminal actor and
        // its detached caller. They share this per-call scope, so deliver the
        // first start only while leaving distinct task ids independent.
        if let ToolNotification::BashExecutionBackgrounded(backgrounded) = &notification
            && let Some(scope) = &self.scope
            && !scope
                .state
                .lock()
                .announced_tasks
                .insert(backgrounded.task_id.clone())
        {
            return;
        }
        let last = self.targets.len().saturating_sub(1);
        let mut notification = Some(notification);
        for (index, target) in self.targets.iter().enumerate() {
            let notification = if index == last {
                let Some(notification) = notification.take() else {
                    break;
                };
                notification
            } else {
                let Some(notification) = notification.as_ref() else {
                    break;
                };
                notification.clone()
            };
            match target {
                ToolNotificationTarget::Plain(target) => {
                    let _ = target.send(notification);
                }
                ToolNotificationTarget::Bounded(target) => {
                    if target.try_send(notification).is_err() {
                        tracing::warn!("tool notification queue full; dropping newest event");
                    }
                }
                ToolNotificationTarget::Capped(target) => target.push(notification),
                ToolNotificationTarget::Acknowledged(target) => {
                    let _ = target.send(AcknowledgedToolNotification {
                        notification,
                        acknowledgement: None,
                    });
                }
                ToolNotificationTarget::Bridge(target) => {
                    let start_ack = if matches!(
                        &notification,
                        ToolNotification::BashExecutionBackgrounded(_)
                    ) {
                        self.scope.as_ref().and_then(|scope| {
                            let (ack, receipt) = tokio::sync::oneshot::channel();
                            scope.register(receipt).ok().map(|()| ack)
                        })
                    } else {
                        None
                    };
                    let _ = target.sender.send(BridgeToolNotification::Notification(
                        AcknowledgedToolNotification {
                            notification,
                            acknowledgement: None,
                        },
                        start_ack,
                        self.scope.is_some(),
                    ));
                }
            }
        }
    }

    /// Send a removal to every target and collect all durable acknowledgements.
    pub fn send_scheduled_task_removed_acknowledged(
        &self,
        removed: ScheduledTaskRemoved,
    ) -> NotificationAcknowledgementBatch {
        let notification = ToolNotification::ScheduledTaskRemoved(removed);
        let mut batch = NotificationAcknowledgementBatch {
            receipts: Vec::new(),
            dispatch_closed: 0,
        };
        for target in self.targets.iter() {
            match target {
                ToolNotificationTarget::Plain(target) => {
                    let _ = target.send(notification.clone());
                }
                ToolNotificationTarget::Bounded(target) => {
                    if target.try_send(notification.clone()).is_err() {
                        tracing::warn!("tool notification queue full; dropping newest event");
                    }
                }
                ToolNotificationTarget::Capped(target) => target.push(notification.clone()),
                ToolNotificationTarget::Acknowledged(target) => {
                    let (acknowledgement, receipt) = tokio::sync::oneshot::channel();
                    if target
                        .send(AcknowledgedToolNotification {
                            notification: notification.clone(),
                            acknowledgement: Some(acknowledgement),
                        })
                        .is_ok()
                    {
                        batch.receipts.push(receipt);
                    } else {
                        batch.dispatch_closed += 1;
                    }
                }
                ToolNotificationTarget::Bridge(target) => {
                    let (acknowledgement, receipt) = tokio::sync::oneshot::channel();
                    if target
                        .sender
                        .send(BridgeToolNotification::Notification(
                            AcknowledgedToolNotification {
                                notification: notification.clone(),
                                acknowledgement: Some(acknowledgement),
                            },
                            None,
                            false,
                        ))
                        .is_ok()
                    {
                        batch.receipts.push(receipt);
                    } else {
                        batch.dispatch_closed += 1;
                    }
                }
            }
        }
        batch
    }

    convenience_sends! {
        send_output_chunk, BashOutputChunk, BashOutputChunk;
        send_complete, BashExecutionComplete, BashExecutionComplete;
        send_timeout, BashExecutionTimeout, BashExecutionTimeout;
        send_backgrounded, BashExecutionBackgrounded, BashExecutionBackgrounded;
        send_failed, BashExecutionFailed, BashExecutionFailed;
        send_file_written, FileWritten, FileWritten;
        send_task_complete, TaskSnapshot, TaskCompleted;
        send_subagent_completed, SubagentCompleted, SubagentCompleted;
        send_plan_mode_entered, PlanModeEntered, PlanModeEntered;
        send_plan_mode_exited, PlanModeExited, PlanModeExited;
        send_user_question_asked, UserQuestionAsked, UserQuestionAsked;
        send_lsp_starting, LspServerStarting, LspServerStarting;
        send_lsp_ready, LspServerReady, LspServerReady;
        send_lsp_crashed, LspServerCrashed, LspServerCrashed;
        send_lsp_retrying, LspServerRetrying, LspServerRetrying;
        send_lsp_failed, LspServerFailed, LspServerFailed;
        send_scheduled_task_fired, ScheduledTaskFired, ScheduledTaskFired;
        send_scheduled_task_removed, ScheduledTaskRemoved, ScheduledTaskRemoved;
        send_scheduled_task_created, ScheduledTaskCreated, ScheduledTaskCreated;
        send_monitor_event, MonitorEvent, MonitorEvent;
    }
}

/// Per-call notification override applied in addition to the session-wide sink.
#[derive(Clone)]
pub struct PerCallNotificationSink(pub ToolNotificationHandle);

#[cfg(test)]
#[path = "handle_tests.rs"]
mod tests;
