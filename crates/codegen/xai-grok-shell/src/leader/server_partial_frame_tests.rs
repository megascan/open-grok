use super::*;
use std::pin::Pin;
use std::task::{Context, Poll};
use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt, DuplexStream, ReadBuf};

/// Signals only after the requested bytes have been consumed and the next read
/// has returned Pending. This makes the outbound/read interleaving deterministic.
struct ObservedStream {
    inner: DuplexStream,
    consumed: Arc<AtomicUsize>,
    barrier_at: Arc<AtomicUsize>,
    barrier_tx: mpsc::UnboundedSender<()>,
}

impl AsyncRead for ObservedStream {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<std::io::Result<()>> {
        let before = buf.filled().len();
        match Pin::new(&mut self.inner).poll_read(cx, buf) {
            Poll::Ready(result) => {
                self.consumed
                    .fetch_add(buf.filled().len() - before, Ordering::SeqCst);
                Poll::Ready(result)
            }
            Poll::Pending => {
                let target = self.barrier_at.load(Ordering::SeqCst);
                if target != usize::MAX
                    && self.consumed.load(Ordering::SeqCst) >= target
                    && self
                        .barrier_at
                        .compare_exchange(target, usize::MAX, Ordering::SeqCst, Ordering::SeqCst)
                        .is_ok()
                {
                    let _ = self.barrier_tx.send(());
                }
                Poll::Pending
            }
        }
    }
}

impl AsyncWrite for ObservedStream {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<std::io::Result<usize>> {
        Pin::new(&mut self.inner).poll_write(cx, buf)
    }

    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Pin::new(&mut self.inner).poll_flush(cx)
    }

    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Pin::new(&mut self.inner).poll_shutdown(cx)
    }
}

struct Fixture {
    client: DuplexStream,
    outbound: AsyncSender<ClientOutbound>,
    events: AsyncReceiver<ServerEvent>,
    consumed: Arc<AtomicUsize>,
    barrier_at: Arc<AtomicUsize>,
    barrier_rx: mpsc::UnboundedReceiver<()>,
    cancel: CancellationToken,
    task: tokio::task::JoinHandle<Result<(), ProtocolError>>,
}

impl Fixture {
    async fn new() -> Self {
        let (mut client, server) = tokio::io::duplex(4096);
        let consumed = Arc::new(AtomicUsize::new(0));
        let barrier_at = Arc::new(AtomicUsize::new(usize::MAX));
        let (barrier_tx, barrier_rx) = mpsc::unbounded_channel();
        let server = ObservedStream {
            inner: server,
            consumed: consumed.clone(),
            barrier_at: barrier_at.clone(),
            barrier_tx,
        };
        let (outbound, outbound_rx) = kanal::unbounded_async();
        let (event_tx, events) = kanal::unbounded_async();
        let cancel = CancellationToken::new();
        let (_, ready_rx) = watch::channel(true);
        let task = tokio::spawn(run_client_session(
            ClientId(42),
            server,
            outbound_rx,
            event_tx,
            cancel.clone(),
            ready_rx,
            default_test_control_state(Path::new("test.sock")),
        ));
        write_message(
            &mut client,
            &ClientMessage::Register {
                client_type: "test".into(),
                mode: ClientMode::Stdio,
                capabilities: ClientCapabilities::default(),
            },
        )
        .await
        .unwrap();
        let registered: ServerMessage = read_message(&mut client).await.unwrap();
        assert!(matches!(registered, ServerMessage::Registered { .. }));
        assert!(matches!(
            events.recv().await.unwrap(),
            ServerEvent::Registered(..)
        ));
        Self {
            client,
            outbound,
            events,
            consumed,
            barrier_at,
            barrier_rx,
            cancel,
            task,
        }
    }

    async fn write_partial_and_wait(&mut self, bytes: &[u8]) {
        assert!(!bytes.is_empty());
        let target = self.consumed.load(Ordering::SeqCst) + bytes.len();
        self.barrier_at.store(target, Ordering::SeqCst);
        self.client.write_all(bytes).await.unwrap();
        tokio::time::timeout(Duration::from_secs(5), self.barrier_rx.recv())
            .await
            .expect("server did not consume the partial frame")
            .expect("read barrier closed");
    }

    async fn send_outbound(&mut self, payload: &str) {
        self.outbound
            .send(ClientOutbound::Acp(Arc::from(payload)))
            .await
            .unwrap();
        let message: ServerMessage =
            tokio::time::timeout(Duration::from_secs(5), read_message(&mut self.client))
                .await
                .expect("outbound frame was not written")
                .unwrap();
        assert!(matches!(message, ServerMessage::Acp { payload: got } if got == payload));
    }

    async fn expect_acp(&self, expected: &str) {
        let event = tokio::time::timeout(Duration::from_secs(5), self.events.recv())
            .await
            .expect("inbound frame was not forwarded")
            .unwrap();
        assert!(
            matches!(event, ServerEvent::Message(ClientId(42), ClientMessage::Acp { payload }) if payload == expected)
        );
    }

    async fn expect_stopped(self) {
        tokio::time::timeout(Duration::from_secs(5), self.task)
            .await
            .expect("client handler did not stop")
            .expect("client handler panicked")
            .expect("client handler returned an error");
        assert!(!matches!(self.events.try_recv(), Ok(Some(_))));
    }
}

fn frame(message: &ClientMessage) -> Vec<u8> {
    let body = serde_json::to_vec(message).unwrap();
    let mut framed = (body.len() as u32).to_be_bytes().to_vec();
    framed.extend_from_slice(&body);
    framed
}

async fn partial_frame_survives_outbound(split: usize) {
    let mut fixture = Fixture::new().await;
    let payload = r#"{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s"}}"#;
    let inbound = frame(&ClientMessage::Acp {
        payload: payload.into(),
    });
    assert!(split < inbound.len());
    fixture.write_partial_and_wait(&inbound[..split]).await;
    for payload in ["first", "second", "third"] {
        fixture.send_outbound(payload).await;
    }
    fixture.client.write_all(&inbound[split..]).await.unwrap();
    fixture.expect_acp(payload).await;

    // A second inbound frame must begin at the correct boundary. Ping and its
    // Pong also exercise the bidirectional protocol after the fragmented frame.
    let next = "another inbound message";
    write_message(
        &mut fixture.client,
        &ClientMessage::Acp {
            payload: next.into(),
        },
    )
    .await
    .unwrap();
    fixture.expect_acp(next).await;
    write_message(&mut fixture.client, &ClientMessage::Ping)
        .await
        .unwrap();
    let pong: ServerMessage = read_message(&mut fixture.client).await.unwrap();
    assert!(matches!(pong, ServerMessage::Pong));
    write_message(&mut fixture.client, &ClientMessage::Disconnect)
        .await
        .unwrap();
    fixture.expect_stopped().await;
}

#[tokio::test]
async fn partial_header_survives_repeated_outbound() {
    partial_frame_survives_outbound(2).await;
}

#[tokio::test]
async fn partial_body_survives_repeated_outbound() {
    partial_frame_survives_outbound(8).await;
}

#[tokio::test]
async fn eof_during_partial_frame_after_outbound_stops_handler() {
    for split in [2, 8] {
        let mut fixture = Fixture::new().await;
        let inbound = frame(&ClientMessage::Acp {
            payload: "incomplete".into(),
        });
        fixture.write_partial_and_wait(&inbound[..split]).await;
        fixture.send_outbound("outbound").await;
        fixture.client.shutdown().await.unwrap();
        fixture.expect_stopped().await;
    }
}

#[tokio::test]
async fn oversized_header_after_outbound_is_rejected() {
    let mut fixture = Fixture::new().await;
    let oversized = (64_u32 * 1024 * 1024 + 1).to_be_bytes();
    fixture.write_partial_and_wait(&oversized[..2]).await;
    fixture.send_outbound("outbound").await;
    fixture.client.write_all(&oversized[2..]).await.unwrap();
    fixture.expect_stopped().await;
}

#[tokio::test]
async fn cancellation_during_partial_frame_stops_handler() {
    let mut fixture = Fixture::new().await;
    let inbound = frame(&ClientMessage::Acp {
        payload: "incomplete".into(),
    });
    fixture.write_partial_and_wait(&inbound[..8]).await;
    fixture.send_outbound("outbound").await;
    fixture.cancel.cancel();
    fixture.expect_stopped().await;
}
