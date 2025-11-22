# AI 開発者向けメモ

このリポジトリは「音声字幕ボット」と「Codex CLI 連携チャット」を一体で提供します。責務ごとにパッケージを分割しているので、改修時はパイプラインをまたがないようにしてください。

## エントリポイント
- `cmd/bot/main.go`  
  - `.env` 読み込み → `config.Load()` → Whisper クライアント作成 → Codex セッションストア読み込み → Bot 起動。

## 音声字幕パイプライン
- `internal/discordbot/bot.go`  
  - Slash コマンド `/join` `/leave` を受け取り、`voice.Manager` を操作。
- `internal/voice/manager.go`  
  - Discord VC への参加/退出、Opus 受信の開始。
  - `audio.Segmenter` で 1 秒無音区切り、WAV 化して `whisper.Client` へ送信。
  - `transcript.Aggregator` で 2 分間編集ウィンドウ付き投稿。
- `internal/whisper/client.go`  
  - faster-whisper-server の `/v1/audio/transcriptions` に multipart で WAV を送信（`FWS_BASE_URL` デフォルト `http://localhost:8000`）。

## Codex チャットパイプライン
- `internal/chat/manager.go`  
  - Slash コマンド `/chat`, `/reset`, `/thread` を登録・処理。
  - `/chat`: チャンネルごとの Codex セッションを取得/生成して返信、セッション ID を永続化。
  - `/reset`: チャンネルのセッション ID を削除。
  - `/thread`: Discord スレッドを作成 → 初回メッセージ送信 → スレッド内メッセージを自動送信・返信。GEMINI_API_KEY があればスレッド名生成を依頼、無ければ日時+IDで命名。
  - スレッド内でユーザーがメッセージを送ると自動で Codex に転送（スラッシュコマンド不要）。
- `internal/codex/client.go`  
  - `codex exec [resume <session>] --json --color never --dangerously-bypass-approvals-and-sandbox <prompt>` を実行。JSON ラインをパースしてテキストと session_id を抽出。
- `internal/codex/store.go`  
  - `CODEX_STATE_PATH`（デフォルト `data/codex_sessions.json`）にチャンネル/スレッドごとの session_id を保存。
- `internal/codex/namer.go`  
  - GEMINI_API_KEY があれば Gemini API でスレッド名を生成。無ければ `thread-YYYYMMDD-HHmm-<id末尾8桁>`。

## コマンド一覧
- `/join` / `/leave` … VC への参加/退出（音声字幕）
- `/chat {message}` / `/start {message}` … チャンネル単位で Codex と会話。セッション永続化。
- `/reset` … チャンネルの Codex セッションを破棄。
- `/thread {message}` … Discord スレッドを作成し、そのスレッド内で自動的に会話継続。

## 環境変数（主要）
- `DISCORD_TOKEN`, `TRANSCRIPT_CHANNEL_ID`
- `FWS_BASE_URL`（デフォルト `http://localhost:8000`）
- `CODEX_STATE_PATH`（デフォルト `data/codex_sessions.json`）
- `CODEX_MODEL`（デフォルト `gpt-5.1`）
- `CODEX_REASONING_EFFORT`（デフォルト `minimal`。空なら Codex デフォルト）
- `GEMINI_API_KEY`（任意、スレッド名生成用）

## テスト・ビルド
- `go test ./...`（`layeh.com/gopus` の C 警告が出ても完走します）
- 音声処理と Codex CLI は外部依存のため、ユニットテストは限定的です。CLI や API へのネットワーク/プロセス実行はモック化せず、ローカル確認を想定。

## 改修時の注意
- 音声とチャットは疎結合に維持してください（音声側が Codex を呼ばない、チャット側が Whisper を触らない）。
- セッション永続化ファイルのスキーマを変更する場合は後方互換を考慮。
- Codex CLI の出力フォーマットが変わる可能性があるため、`internal/codex/client.go` のパーサを優先的に修正してください。
