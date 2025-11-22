# discord-realtime-voice2text-bot

Discord の VC 音声をリアルタイムに文字起こししつつ、Codex CLI と連携した会話ができる Go 製ボットです。音声字幕パイプラインとチャットパイプラインを分離し、`/join` `/leave` `/chat` `/start` `/reset` `/thread` を提供しています。

## できること

- 音声字幕: `/join` でボイスチャンネルに参加し、1 秒無音で区切った発話を faster-whisper に送信。`<表示名>: 「テキスト」` 形式で 2 分間は同一メッセージを編集で追記し、それ以降は確定。
- チャンネル会話: テキストチャンネルで `/chat {メッセージ}` を送ると、そのチャンネル専用の Codex スレッドを作成・継続利用。セッション ID はファイルに保存され、再起動後も継続します。
- リセット: `/reset` でそのチャンネルの Codex セッション ID を破棄。次の `/chat` から新規会話。
- スレッド会話: `/thread {メッセージ}` で Discord スレッドを作成し、以降そのスレッド内の発言は自動で Codex に送信・返信。GEMINI_API_KEY があればプロンプト内容からスレッド名を生成、無ければ作成日時+IDで命名。
- どちらのパイプラインもエラー時はログ出力してスキップし、ボット全体は停止しません。

## 必要要件

- Go 1.22 以降（モジュールは 1.24 で初期化済み）
- Docker（`fedirz/faster-whisper-server:latest-cpu` をローカルで起動）
- Codex CLI（すでに認証済みで `codex exec ...` が動作すること）
- Discord Bot アカウントと Intent
  - `MESSAGE CONTENT INTENT`
  - `GUILD VOICE STATES INTENT`
- 権限: VC 参加/受信・スレッド作成・対象テキストチャンネルへの投稿
- オプション: `GEMINI_API_KEY`（スレッド名自動生成用、未設定なら日時ベースの名前）

## セットアップ

### 1. faster-whisper-server を起動

```fish
docker run --publish 8000:8000 \
  --volume ~/.cache/huggingface:/root/.cache/huggingface \
  fedirz/faster-whisper-server:latest-cpu
```

- デフォルト接続先は `http://localhost:8000`（`FWS_BASE_URL` 未設定時）。
- Bot は `/v1/audio/transcriptions` に `file=@segment.wav`（`language=ja`）を送信して文字起こしします。

### 2. 環境変数を用意（`.env` で自動読み込み可）

| 変数名 | 必須 | デフォルト | 説明 |
| ------ | ---- | ---------- | ---- |
| `DISCORD_TOKEN` | ✅ | - | Discord Bot Token |
| `TRANSCRIPT_CHANNEL_ID` | ✅ | - | 字幕をまとめて投稿するテキストチャンネル ID |
| `FWS_BASE_URL` | ❌ | `http://localhost:8000` | faster-whisper-server のベース URL |
| `CODEX_STATE_PATH` | ❌ | `data/codex_sessions.json` | Codex セッション ID 永続化ファイル |
| `CODEX_MODEL` | ❌ | `gpt-5.1` | Codex に渡すモデル名 |
| `CODEX_REASONING_EFFORT` | ❌ | `minimal` | Codex の reasoning effort（設定が無ければ Codex デフォルト） |
| `GEMINI_API_KEY` | ❌ | - | スレッド名生成に使用（未設定なら日時+IDで命名） |

Fish で直接設定する例:

```fish
set -x DISCORD_TOKEN your-token
set -x TRANSCRIPT_CHANNEL_ID 123456789012345678
set -x FWS_BASE_URL http://localhost:8000
set -x CODEX_STATE_PATH data/codex_sessions.json
# set -x GEMINI_API_KEY your-gemini-key  # 必要な場合のみ
```

### 3. Bot を起動（すべて fish を想定）

```fish
go run ./cmd/bot
```

ビルドしてから実行する場合:

```fish
go build -o bin/discord-realtime-voice2text-bot ./cmd/bot
./bin/discord-realtime-voice2text-bot
```

テスト（C コンパイラ警告が出ても完走します）:

```fish
go test ./...
```

## 使い方

### 音声字幕コマンド
- `/join`: コマンド送信者がいる VC に参加し、全員の音声を受信開始。
- `/leave`: 退出して音声受信と文字起こしを停止。

### チャットコマンド
- `/chat {メッセージ}`（`/start` も同じ動き）: 現在のテキストチャンネルで Codex と会話。セッションはチャンネルごとに保存され、再起動後も継続。
- `/reset`: そのチャンネルの Codex セッションを破棄。次回 `/chat` で新規開始。
- `/thread {メッセージ}`: 新しい Discord スレッドを作成し、以降スレッド内の発言を自動で Codex に送信して返信。スレッド内では `/chat` は不要。GEMINI_API_KEY があればプロンプト内容からスレッド名を生成。

### 音声パイプラインの流れ
1. SSRC ごとに Opus を受信 → PCM16 (48kHz/Mono) へデコード。
2. 1 秒無音で発話区切り。250ms 未満または平均振幅が低いセグメントは破棄。
3. セグメントを WAV 化し faster-whisper-server に送信、`text` を取得。
4. `<表示名>: 「テキスト」` の 1 行に整形。
5. `TRANSCRIPT_CHANNEL_ID` へ投稿。直近 2 分は同一メッセージを編集で追記、2 分間無音なら確定。

## ディレクトリ構成（抜粋）

```
cmd/bot/main.go        エントリポイント（設定読み込み + Bot 起動）
internal/config        環境変数読み込み
internal/discordbot    Discord セッションとイベントルーティング
internal/voice         /join, /leave の音声受信〜文字起こしの管理
internal/audio         Opus 受信・無音判定・WAV 書き出し
internal/transcript    2 分ウィンドウのメッセージ集約
internal/whisper       faster-whisper-server クライアント
internal/codex         Codex CLI 呼び出し + セッション永続化 + スレッド命名
internal/chat          /chat, /reset, /thread ハンドラと Codex 連携
third_party/discordgo  フォーク済み discordgo（SSRC ログ対応）
third_party/discord-codex-bot  参考実装（コードは変更しません）
```

## 運用メモ

- Bot 停止は実行プロセスへ `Ctrl+C`。サービス化する場合は `systemd` 等でラップしてください。
- `FWS_BASE_URL` を変えるとリモートの faster-whisper-server に接続できます。
- Codex セッションは `CODEX_STATE_PATH` に保存されます。不要になったらファイルを削除/バックアップしてください。
- `/thread` のスレッド名は GEMINI_API_KEY があれば内容ベース、無ければ日時+ID になります。
