# Whisper Discord Bot

Discord のボイスチャンネルで話された音声をローカルの `faster-whisper-server` に送信し、文字起こし結果を特定のテキストチャンネルに 2 分間の編集ウィンドウで集約して投稿する Bot です。

## 必要要件

- Go 1.22 以降
- Docker（`fedirz/faster-whisper-server:latest-cpu` を使用）
- Discord Bot アカウント（メッセージコンテンツ/ボイスステートの Intent を有効化）

## faster-whisper-server の起動

`faster-whisper-server` を CPU 版 Docker イメージで起動してから Bot を動かしてください。

```fish
docker run --publish 8000:8000 --volume ~/.cache/huggingface:/root/.cache/huggingface \
  fedirz/faster-whisper-server:latest-cpu
```

サーバーは `http://localhost:8000` で待ち受けるため、`FWS_BASE_URL` を省略した場合はこの URL がデフォルトで利用されます。

## 環境変数の設定（.env 推奨）

プロジェクト直下にある `.env.example` をコピーし、Bot のトークン等を記入します。

```fish
cp .env.example .env
```

`.env` を編集して以下の値を設定します。

- `DISCORD_TOKEN` : Discord Bot Token
- `TRANSCRIPT_CHANNEL_ID` : 文字起こしを投稿するテキストチャンネル ID
- `FWS_BASE_URL` : faster-whisper-server のエンドポイント（未設定時は `http://localhost:8000`）

`go run ./cmd/bot` や `go build ./cmd/bot` は `.env` を自動で読み込むため、追加の環境変数設定は不要です。もちろん、必要があれば従来どおり `set -x` で上書きできます。

## 起動方法

依存関係を取得後、単純に以下を実行してください。

```fish
go run ./cmd/bot
```

### ビルドだけ行う場合

```fish
go build ./cmd/bot
```

## テストの実行

```fish
go test ./...
```

## 動作概要

1. `!join` コマンドで、コマンド送信者が参加している VC に Bot が参加します。
2. VC の音声を受信し、ユーザーごとに 1 秒の無音で区切ったセグメントを WAV に変換します。
3. 各セグメントを `faster-whisper-server` (`/v1/audio/transcriptions`, `language=ja`) へ送信し、文字起こしを取得します。
4. `TRANSCRIPT_CHANNEL_ID` で指定したテキストチャンネルに、表示名 + 「発話内容」の形式で投稿します。
5. 投稿後 2 分以内に新しい発話があれば同じメッセージを編集し、それ以降は新しいメッセージとして投稿します。

`!leave` コマンドで VC から退出します。
