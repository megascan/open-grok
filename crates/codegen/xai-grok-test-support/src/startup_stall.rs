//! Startup fetch controls for the loopback inference fixture.
use std::sync::RwLock;
use std::sync::atomic::{AtomicU32, Ordering};
use std::time::Duration;

#[derive(Clone, Copy, Default)]
enum Stall {
    #[default]
    None,
    Hang,
    Delay(Duration),
}

#[derive(Default)]
pub(crate) struct StartupStall {
    stall: RwLock<Stall>,
    served: AtomicU32,
}

impl StartupStall {
    pub(crate) fn set_hang(&self, enabled: bool) {
        *self.stall.write().unwrap() = if enabled { Stall::Hang } else { Stall::None };
    }

    pub(crate) fn set_delay(&self, duration: Duration) {
        *self.stall.write().unwrap() = Stall::Delay(duration);
    }

    pub(crate) fn clear(&self) {
        *self.stall.write().unwrap() = Stall::None;
    }

    pub(crate) fn served(&self) -> u32 {
        self.served.load(Ordering::SeqCst)
    }

    pub(crate) async fn apply(&self) {
        let stall = *self.stall.read().unwrap();
        match stall {
            Stall::None => {}
            Stall::Hang => {
                std::future::pending::<()>().await;
            }
            Stall::Delay(delay) => {
                tokio::time::sleep(delay).await;
                self.served.fetch_add(1, Ordering::SeqCst);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test(start_paused = true)]
    async fn a_completed_delay_is_counted_and_a_cancelled_hang_is_not() {
        let state = StartupStall::default();
        state.set_hang(true);
        assert!(
            tokio::time::timeout(Duration::from_secs(1), state.apply())
                .await
                .is_err()
        );
        assert_eq!(state.served(), 0);
        state.set_delay(Duration::from_secs(2));
        state.apply().await;
        assert_eq!(state.served(), 1);
        state.clear();
        state.apply().await;
        assert_eq!(state.served(), 1);
    }
}
