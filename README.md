# discord-whisper-transcriber

> 本リポジトリ（現在の `whisper-discord-bot`）を公開する際の提案名は **`discord-whisper-transcriber`** です。Bot の役割である「Discord で話された内容を即座に文字起こししてまとめる」ことを端的に表現しています。

## できること（v1 機能一覧）

- Discord ボイスチャンネル (VC) へ `!join` で参加し、!leave で退出。
- VC の音声を SSRC 単位で受信し、Opus → PCM16 → WAV に変換。
- ローカルで稼働している `faster-whisper-server` に各セグメントを `language=ja` で送信し文字起こし。
- 1 秒間の無音でユーザーごとに発話を区切り、`<表示名>: 「テキスト」` 形式で投稿。
- 指定テキストチャンネルに 2 分間編集ウィンドウ付きで集約投稿（2 分以内の発話は同一メッセージを編集、2 分間無音で確定）。
- 失敗時もログへ記録しつつセグメント単位でリトライせずに継続動作。

## 必要要件

- Go 1.22 以降（プロジェクトは 1.24 でモジュール初期化済）
- Docker（`fedirz/faster-whisper-server:latest-cpu` をローカル起動）
- Discord Bot アカウント（以下の Intent を有効化）
  - `MESSAGE CONTENT INTENT`
  - `GUILD VOICE STATES INTENT`
- VC への接続・音声受信/送信権限、ターゲットテキストチャンネルへの投稿権限

## プロジェクト構成ハイライト

```
cmd/bot/main.go        エントリポイント（設定読み込み + Bot 起動）
internal/config        環境変数管理
internal/discordbot    Discord セッション、コマンド、VC 制御
internal/audio         Opus 受信、SSRC 解析、無音区切りセグメンタ
internal/transcript    2 分メッセージ集約ロジック
internal/whisper       faster-whisper-server クライアント
third_party/discordgo  SSRC デバッグを含むフォーク済み discordgo
```

## faster-whisper-server の起動

Bot より先に `faster-whisper-server` を起動してください。GPU が無い環境を想定し、Docker で CPU 版を使用します。

```fish
docker run --publish 8000:8000 \
  --volume ~/.cache/huggingface:/root/.cache/huggingface \
  fedirz/faster-whisper-server:latest-cpu
```

- デフォルトベース URL は `http://localhost:8000`。環境変数 `FWS_BASE_URL` が未設定の場合に使用します。
- Bot は `/v1/audio/transcriptions` エンドポイントに multipart/form-data で `file=@segment.wav` を送信し、OpenAI 互換レスポンス (`{"text":"..."}`) から文字起こし結果を取得します。

## 環境変数の設定

`.env` を置くと自動で読み込まれるため、開発時は `.env.example` をコピーして利用するのが最も簡単です。

```fish
cp .env.example .env
```

| 変数名 | 必須 | 説明 |
| ------ | ---- | ---- |
| `DISCORD_TOKEN` | ✅ | Discord Bot Token。Bot を実行する PC にのみ保持してください。 |
| `TRANSCRIPT_CHANNEL_ID` | ✅ | 文字起こし結果を投稿するテキストチャンネル ID。集約メッセージの送信先です。 |
| `FWS_BASE_URL` | ❌ | `faster-whisper-server` のベース URL。未設定時は `http://localhost:8000`。 |

Fish シェルから直接起動したい場合の例（`.env` を使わない場合）：

```fish
set -x DISCORD_TOKEN your-token-here
set -x TRANSCRIPT_CHANNEL_ID 123456789012345678
set -x FWS_BASE_URL http://localhost:8000
```

## 実行方法（すべて Fish シェル）

依存インストール → Bot 起動:

```fish
go run ./cmd/bot
```

ビルド済バイナリを作る場合:

```fish
go build -o bin/discord-whisper ./cmd/bot
./bin/discord-whisper
```

## テスト

```fish
go test ./...
```

（`layeh.com/gopus` 由来の C コンパイラ警告が出ることがありますが、ビルド・テストは完了します。）

## Bot コマンドと挙動

| コマンド | 送信場所 | 挙動 |
| -------- | -------- | ---- |
| `!join`  | 任意のテキストチャンネル | コマンド送信者が参加中の VC を検出し、Bot が参加。成功するとテキストチャンネルへ「参加しました。」と通知。既存参加者を含む全員の音声を即時受信します。 |
| `!leave` | 任意のテキストチャンネル | Bot が VC から退出し、テキストチャンネルへ「退出しました。」と通知。セグメンタや Whisper への送信を停止します。 |

### 音声処理パイプライン

1. VC から受信した Opus パケットを SSRC ごとにデコードし、PCM16 (48kHz/Mono) へ変換。
2. ユーザーごとの無音しきい値（1 秒）で発話を区切る。250ms 未満・平均振幅が低いセグメントはノイズとして破棄。
3. セグメントを WAV に書き出し `faster-whisper-server` にアップロード、JSON の `text` フィールドを取得。
4. 文字起こしは `<表示名>: 「テキスト」` の 1 行に整形。
5. `TRANSCRIPT_CHANNEL_ID` へポスト。直近 2 分以内に追加発話があれば同じメッセージを編集、2 分間追加がないと確定。
6. Discord の Nickname があれば優先表示、無い場合は Username、取得不可の場合は UserID を表示。

### ログとデバッグ

- `third_party/discordgo` に加えた SSRC デバッグログで、Join 時に既存参加者の SSRC マッピング状況を確認できます。
- `internal/audio/receiver` でも SSRC 未解決時のバッファリング、解決後のフラッシュ、`OpusRecv` からのシーケンス番号などを詳細に出力するため、`!join` 直後の挙動分析に利用してください。

## 運用 Tips

- Bot を VC に入れる前に `faster-whisper-server` を必ず起動しておくと、最初のセグメントから即座に文字起こしが開始されます。
- ネットワーク環境によっては WAV のアップロードが詰まる可能性があります。`FWS_BASE_URL` を変更して別ホストのサーバーを指定することで対処できます。
- Bot を手動で停止したい場合は実行中プロセスに `Ctrl+C` を送るか、`systemd`／`nohup` などでデーモン化してください。

## ライセンスと謝辞

- Discord クライアントライブラリには `github.com/bwmarrin/discordgo` をフォークして利用しています（`third_party/discordgo`）。
- 音声デコードは `layeh.com/gopus` を利用しています。
- 文字起こしサーバーは `fedirz/faster-whisper-server:latest-cpu` Docker イメージに依存しています。

上記コンポーネントのライセンスに従い、本リポジトリのコードも必要に応じて表記を行ってください。
