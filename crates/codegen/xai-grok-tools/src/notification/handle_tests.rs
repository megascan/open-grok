use super::*;
use crate::notification::ScheduledTaskRemovedReason;

#[test]
fn revocation_is_exact_to_turn_and_session_instance() {
    let (handle, _receiver) = ToolNotificationHandle::bridge_channel();
    let first_guard = handle.begin_turn_delivery();
    let first = first_guard.scope();
    let deferred = first.child();
    let second_guard = handle.begin_turn_delivery();
    let second = second_guard.scope();
    first.revoke();
    assert!(deferred.is_revoked());
    assert!(!second.is_revoked());
    drop(first_guard);
    handle.revoke_session_deliveries();
    assert!(second.is_revoked());
    let (resumed, _receiver) = ToolNotificationHandle::bridge_channel();
    let resumed_guard = resumed.begin_turn_delivery();
    assert!(!resumed_guard.scope().is_revoked());
}

#[tokio::test]
async fn bridge_barrier_reports_closed_and_dropped_consumers() {
    let (handle, receiver) = ToolNotificationHandle::bridge_channel();
    drop(receiver);
    assert_eq!(
        handle.wait_for_bridge().await,
        Err(NotificationAcknowledgementError::DispatchClosed(1))
    );

    let (handle, mut receiver) = ToolNotificationHandle::bridge_channel();
    let waiter = tokio::spawn(async move { handle.wait_for_bridge().await });
    let BridgeToolNotification::Barrier(acknowledgement) = receiver.recv().await.unwrap() else {
        panic!("expected barrier");
    };
    drop(acknowledgement);
    assert_eq!(
        waiter.await.unwrap(),
        Err(NotificationAcknowledgementError::AcknowledgementDropped(1))
    );
}

#[tokio::test]
async fn failed_fanout_resolves_already_reserved_starts() {
    let (first, mut first_rx) = ToolNotificationHandle::bridge_channel();
    let (closed, closed_rx) = ToolNotificationHandle::bridge_channel();
    drop(closed_rx);
    let tee = ToolNotificationHandle::tee(vec![first, closed]);
    assert!(tee.reserve_background_start("child").is_err());
    let BridgeToolNotification::ExternalStart(receipt) = first_rx.recv().await.unwrap() else {
        panic!("expected start reservation");
    };
    assert!(receipt.await.unwrap().is_err());
}

fn removed(task_id: &str) -> ScheduledTaskRemoved {
    ScheduledTaskRemoved {
        task_id: task_id.into(),
        reason: ScheduledTaskRemovedReason::Deleted,
        generation: String::new(),
        revision: 0,
    }
}

fn created(task_id: &str) -> ScheduledTaskCreated {
    ScheduledTaskCreated {
        task_id: task_id.into(),
        prompt: task_id.into(),
        human_schedule: "every 5 minutes".into(),
        next_fire_at: None,
        generation: String::new(),
        revision: 0,
    }
}

fn task_id(notification: &ToolNotification) -> &str {
    match notification {
        ToolNotification::ScheduledTaskCreated(value) => &value.task_id,
        ToolNotification::ScheduledTaskRemoved(value) => &value.task_id,
        other => panic!("unexpected notification: {other:?}"),
    }
}

#[tokio::test]
async fn acknowledged_removal_stays_in_fifo() {
    let (handle, mut receiver) = ToolNotificationHandle::acknowledged_channel();
    handle.send_scheduled_task_created(created("before"));
    let batch = handle.send_scheduled_task_removed_acknowledged(removed("deleted"));
    handle.send_scheduled_task_created(created("after"));

    let first = receiver.recv().await.unwrap();
    assert_eq!(task_id(&first.notification), "before");
    assert!(first.acknowledgement.is_none());
    let second = receiver.recv().await.unwrap();
    assert_eq!(task_id(&second.notification), "deleted");
    second.acknowledgement.unwrap().send(Ok(())).unwrap();
    let third = receiver.recv().await.unwrap();
    assert_eq!(task_id(&third.notification), "after");
    assert!(third.acknowledgement.is_none());

    batch.wait().await.unwrap();
}

#[tokio::test]
async fn mixed_fanout_attempts_every_target_before_reporting_closed_dispatch() {
    let (closed, closed_rx) = ToolNotificationHandle::acknowledged_channel();
    drop(closed_rx);
    let (plain, mut plain_rx) = ToolNotificationHandle::channel();
    let (durable, mut durable_rx) = ToolNotificationHandle::acknowledged_channel();
    let handle = ToolNotificationHandle::tee(vec![closed, plain, durable]);

    handle.send_scheduled_task_created(created("before"));
    let batch = handle.send_scheduled_task_removed_acknowledged(removed("deleted"));
    handle.send_scheduled_task_created(created("after"));

    assert_eq!(task_id(&plain_rx.recv().await.unwrap()), "before");
    assert_eq!(task_id(&plain_rx.recv().await.unwrap()), "deleted");
    assert_eq!(task_id(&plain_rx.recv().await.unwrap()), "after");
    let durable_before = durable_rx.recv().await.unwrap();
    assert_eq!(task_id(&durable_before.notification), "before");
    let durable_removed = durable_rx.recv().await.unwrap();
    assert_eq!(task_id(&durable_removed.notification), "deleted");
    durable_removed
        .acknowledgement
        .unwrap()
        .send(Ok(()))
        .unwrap();
    let durable_after = durable_rx.recv().await.unwrap();
    assert_eq!(task_id(&durable_after.notification), "after");

    assert_eq!(
        batch.wait().await,
        Err(NotificationAcknowledgementError::DispatchClosed(1))
    );
}

#[tokio::test]
async fn batch_distinguishes_dropped_and_rejected_acknowledgements() {
    let (dropped, mut dropped_rx) = ToolNotificationHandle::acknowledged_channel();
    let (rejected, mut rejected_rx) = ToolNotificationHandle::acknowledged_channel();
    let handle = ToolNotificationHandle::tee(vec![dropped, rejected]);
    let batch = handle.send_scheduled_task_removed_acknowledged(removed("deleted"));

    drop(dropped_rx.recv().await.unwrap().acknowledgement);
    rejected_rx
        .recv()
        .await
        .unwrap()
        .acknowledgement
        .unwrap()
        .send(Err("rejected".into()))
        .unwrap();

    assert_eq!(
        batch.wait().await,
        Err(NotificationAcknowledgementError::Multiple {
            dispatch_closed: 0,
            acknowledgements_dropped: 1,
            consumer_rejections: vec!["rejected".into()],
        })
    );
}

#[tokio::test]
async fn bounded_channel_drops_newest_when_full() {
    let (handle, mut receiver) = ToolNotificationHandle::bounded_channel(1);
    handle.send_scheduled_task_created(created("kept"));
    handle.send_scheduled_task_created(created("dropped"));

    assert_eq!(task_id(&receiver.recv().await.unwrap()), "kept");
    assert!(receiver.try_recv().is_err());
}

#[tokio::test]
async fn capped_channel_evicts_lossy_event_for_terminal_event() {
    let (handle, mut receiver) = ToolNotificationHandle::capped_channel(1);
    handle.send_scheduled_task_created(created("lossy"));
    handle.send(ToolNotification::ScheduledTaskRemoved(removed("terminal")));

    assert_eq!(task_id(&receiver.recv().await.unwrap()), "terminal");
}

#[tokio::test]
async fn admissions_have_independent_receipts_and_shutdown_terminals() {
    let (handle, mut receiver) = ToolNotificationHandle::bridge_channel();
    let mut first = handle.reserve_subagent_admission().unwrap();
    let mut second = handle.reserve_subagent_admission().unwrap();
    let BridgeToolNotification::ExternalStart(first_receipt) = receiver.recv().await.unwrap()
    else {
        panic!("first receipt")
    };
    let BridgeToolNotification::ExternalStart(second_receipt) = receiver.recv().await.unwrap()
    else {
        panic!("second receipt")
    };
    first.mark_enqueued();
    second.mark_enqueued();
    let admitted = first.admit("same-id");
    let BridgeToolNotification::SubagentAdmitted {
        generation,
        acknowledgement,
        ..
    } = receiver.recv().await.unwrap()
    else {
        panic!("admission")
    };
    assert_eq!(admitted.generation(), generation);
    second.reject();
    assert_eq!(second_receipt.await.unwrap(), Ok(()));
    acknowledgement.send(Ok(())).unwrap();
    assert_eq!(first_receipt.await.unwrap(), Ok(()));
    drop(admitted);
    let BridgeToolNotification::SubagentAdmissionEnded {
        id,
        generation: ended,
        error,
    } = receiver.recv().await.unwrap()
    else {
        panic!("terminal")
    };
    assert_eq!(id, "same-id");
    assert_eq!(ended, generation);
    assert!(error.is_none());
}

#[tokio::test]
async fn lost_enqueued_admission_fails_closed_and_fanout_rolls_back() {
    let (handle, mut receiver) = ToolNotificationHandle::bridge_channel();
    let mut reservation = handle.reserve_subagent_admission().unwrap();
    let BridgeToolNotification::ExternalStart(receipt) = receiver.recv().await.unwrap() else {
        panic!("receipt")
    };
    reservation.mark_enqueued();
    drop(reservation);
    assert!(receipt.await.unwrap().is_err());
    let (closed, closed_rx) = ToolNotificationHandle::bridge_channel();
    drop(closed_rx);
    let tee = ToolNotificationHandle::tee(vec![handle, closed]);
    assert!(tee.reserve_subagent_admission().is_err());
    let BridgeToolNotification::ExternalStart(receipt) = receiver.recv().await.unwrap() else {
        panic!("receipt")
    };
    assert!(receipt.await.unwrap().is_err());
}

#[tokio::test]
async fn cancelled_scope_and_late_receipt_do_not_poison_new_turn() {
    let (handle, _receiver) = ToolNotificationHandle::bridge_channel();
    let old_guard = handle.begin_turn_delivery();
    let old_scope = old_guard.scope();
    let old = handle
        .with_delivery_scope(Some(old_scope.clone()))
        .reserve_delivery()
        .unwrap();
    let new_guard = handle.begin_turn_delivery();
    let new_scope = new_guard.scope();
    drop(old_guard);
    assert_eq!(handle.current_delivery_scope().unwrap().id, new_scope.id);
    old.resolve(Err("late old failure".into()));
    let fresh = handle
        .with_delivery_scope(Some(new_scope.clone()))
        .reserve_delivery()
        .unwrap();
    fresh.resolve(Ok(()));
    assert!(new_scope.wait().await.is_ok());
    assert!(
        handle
            .with_delivery_scope(Some(old_scope))
            .reserve_delivery()
            .is_err()
    );
}

#[tokio::test]
async fn delivery_reservation_waits_for_the_actual_start() {
    let (handle, _receiver) = ToolNotificationHandle::bridge_channel();
    let guard = handle.begin_turn_delivery();
    let scope = guard.scope();
    let reservation = handle
        .with_delivery_scope(Some(scope.clone()))
        .reserve_delivery()
        .unwrap();
    let mut waiter = tokio::spawn(async move { scope.wait().await });
    tokio::task::yield_now().await;
    assert!(!waiter.is_finished());
    reservation.resolve(Ok(()));
    assert!((&mut waiter).await.unwrap().is_ok());
}

#[tokio::test]
async fn lost_started_delivery_is_error_but_rejected_launch_is_not() {
    let (handle, _rx) = ToolNotificationHandle::bridge_channel();
    let scope = BackgroundDeliveryScope::new();
    let bound = handle.with_delivery_scope(Some(scope.clone()));
    drop(bound.reserve_delivery().unwrap());
    let mut started = bound.reserve_delivery().unwrap();
    started.mark_started();
    drop(started);
    assert!(scope.wait().await.is_err());
}

#[tokio::test]
async fn unresponsive_client_delivery_has_a_bounded_error() {
    let (handle, _rx) = ToolNotificationHandle::bridge_channel();
    let scope = BackgroundDeliveryScope::new();
    let _reservation = handle
        .with_delivery_scope(Some(scope.clone()))
        .reserve_delivery()
        .unwrap();
    let error = scope
        .wait_bounded(std::time::Duration::from_millis(1))
        .await
        .unwrap_err();
    assert!(error.contains("timed out"));
}

#[tokio::test]
async fn cohort_delivery_failure_is_carried_by_its_terminal() {
    let (handle, mut receiver) = ToolNotificationHandle::bridge_channel();
    let scope = BackgroundDeliveryScope::new();
    let cohort = handle
        .with_delivery_scope(Some(scope.clone()))
        .reserve_subagent_admission()
        .unwrap()
        .admit("swarm:1");
    let BridgeToolNotification::SubagentAdmitted {
        acknowledgement,
        generation,
        ..
    } = receiver.recv().await.unwrap()
    else {
        panic!("expected cohort admission")
    };
    acknowledgement.send(Ok(())).unwrap();
    scope.wait().await.unwrap();
    cohort.report_delivery_failure("client stopped");
    drop(cohort);
    let BridgeToolNotification::SubagentAdmissionEnded {
        id,
        generation: failed,
        error,
    } = receiver.recv().await.unwrap()
    else {
        panic!("expected delivery failure")
    };
    assert_eq!(
        (id.as_str(), failed.as_str(), error.as_deref()),
        ("swarm:1", generation.as_str(), Some("client stopped"))
    );
}
