---
name: renovate-automerge
description: このリポジトリの Renovate PR を調査し、repo固有ルールに従ってマージする時に使う。
---

# Renovate Automerge

この skill は、`wim-web/ding` で Renovate が作成した open PR を確認し、以下のルールに従ってマージまたは報告する。

## 対象PR

- 作成者が Renovate の open PR のみを対象にする。
- このリポジトリで観測した Renovate author: `app/renovate`
- commit author として観測した bot: `renovate[bot]`
- base branch は `main` のみを対象にする。
- head branch は `renovate/*` のみを対象にする。
- Dependabot や人間が作成した PR は対象外。

## repo の前提

- repository: `https://github.com/wim-web/ding.git`
- default branch: `main`
- Renovate 設定: `renovate.json`
- Renovate preset: `local>wim-web/renovate-config`
- Go module: `go.mod`
- Go dependency は標準ライブラリのみで、現時点では `go.sum` は存在しない。
- CI workflow:
  - `.github/workflows/test.yml`: `go test ./...` と `go build -o /tmp/ding-check .`
  - `.github/workflows/release.yml`: tag push / manual dispatch で cross build と GitHub Release publish
- workflow 内の reusable action は SHA 固定し、右側コメントに tag を併記している。
- Docker、infra、deploy、database migration は存在しない。

## 必ず確認すること

明らかなブロック条件があっても、そこで早期終了しない。影響範囲調査を完了してから、マージしてよい / マージしてはいけない / 人間確認が必要、のいずれかを判断する。

- PR title/body
- changed files
- update type
- Renovate がPR本文に載せた release notes / changelog / compatibility notes
- upstream 公式 release notes / changelog / upgrade guide / migration guide
- 破壊的変更、deprecated API、設定変更、peer dependency変更、runtime要件変更の有無
- 影響範囲: Go toolchain / Go module / GitHub Actions / CI / release workflow / CLI runtime behavior
- check status
- merge conflict の有無
- requested changes / 未解決の人間 review comment の有無

## 調査手順

対象 PR ごとに、必ずこの順序で調査してから判定する。

1. `gh pr list --state open --author app/renovate --json number,title,author,baseRefName,headRefName,isDraft,url` で対象候補を確認する。
2. `gh pr view PR番号 --json title,body,author,baseRefName,headRefName,isDraft,mergeable,reviewDecision,files,statusCheckRollup,commits,reviews,comments,url` で PR metadata を確認する。
3. `gh pr diff PR番号 --patch` で差分を確認する。
4. Renovate PR 本文の release notes / changelog / compatibility notes を読む。
5. upstream 公式 release notes / changelog / upgrade guide / migration guide を読む。PR 本文だけで済ませない。
6. repo 内で変更対象の参照箇所を `rg` で検索し、この repo での影響範囲を確認する。
7. 変更後も workflow の `uses:` が `owner/repo@40桁SHA # vX.Y.Z` の形を維持しているか確認する。
8. ここまで完了してから判定する。

## マージしてよいもの

以下をすべて満たす Renovate PR だけを自動マージしてよい。

- 作成者が `app/renovate`。
- commit author が `renovate[bot]`。
- base branch が `main`。
- head branch が `renovate/*`。
- draft ではない。
- mergeable が `MERGEABLE`。
- requested changes や未解決の人間 review comment がない。
- `statusCheckRollup` の check が 1 件以上存在し、すべて成功している。
- changed files が `go.mod`、`go.sum`、`.github/workflows/test.yml`、`.github/workflows/release.yml`、`renovate.json` の範囲に収まる。
- Go toolchain の patch update。
- `.github/workflows/test.yml` / `.github/workflows/release.yml` の `go-version` patch update。
- `actions/checkout` または `actions/setup-go` の GitHub Actions patch / minor update。
- GitHub Actions 更新では、`uses:` が SHA 固定を維持し、右側コメントの tag も更新されている。
- `renovate.json` の更新は、`local>wim-web/renovate-config` 参照を維持したままの schema / formatting / patch-level metadata 更新に限る。
- PR 本文と upstream 公式 changelog / release notes / migration guide の両方を確認し、この repo の CLI runtime behavior、build、test、release workflow に破壊的影響がないと判断できる。

## マージしてはいけないもの

以下のいずれかに該当する PR は自動マージしない。必要に応じて報告または PR コメントを残す。

- 作成者が `app/renovate` ではない。
- base branch が `main` ではない。
- head branch が `renovate/*` ではない。
- major update。
- Go toolchain の minor update。
- `go.mod` の module path 変更。
- 新しい runtime dependency / external module を追加する PR。
- `go.sum` に新しい dependency が追加され、標準ライブラリのみという前提が崩れる PR。
- source code、test code、README、LICENSE、release asset naming、config path、Discord API behavior を変更する PR。
- `.github/workflows/*` の release trigger、permissions、concurrency、publish command、asset naming、ldflags、Go target matrix を変更する PR。
- `renovate.json` が `local>wim-web/renovate-config` を外す、別 preset を追加する、automerge 挙動を変える PR。
- Docker、infra、deploy、database migration を追加または変更する PR。
- breaking changes、deprecated API、runtime 要件変更、設定変更、release workflow 互換性問題の可能性が残る PR。
- changelog / release notes / migration guide を確認できず、影響範囲を判断できない PR。
- failed / pending / missing checks がある PR。
- merge conflict がある PR。
- requested changes がある PR。
- 未解決の人間 review comment / PR comment がある PR。

## check の扱い

- `statusCheckRollup` を確認し、すべて成功している場合だけマージしてよい。
- pending、failed、cancelled、skipped、timed out、missing の check がある場合はマージしない。
- check が一つも報告されていない PR は missing checks として扱い、マージしない。
- branch protection や required check は推測しない。

## マージ方法

- `gh pr merge PR番号 --squash` を使う。
- merge 前に `gh pr view` で最新の `mergeable` と `statusCheckRollup` を再確認する。
- `--auto` は使わない。
- `--delete-branch` は使わない。

## post-merge action

- Renovate PR のマージ後に release、deploy、local tool update、GitHub comment などの追加 action は実行しない。
- 複数 PR をマージした場合も、最後にまとめて実行する追加 action はない。
- この repo の release は明示的な tag push または release workflow の manual dispatch で行う。Renovate PR のマージを理由に release は作らない。

## 報告

作業後に以下を日本語で報告する。

- 対象 Renovate PR がなかった場合は、その旨だけを報告する。
- マージした PR: PR 番号、title、URL、dependency、version change、merge method、check 状態。
- マージしなかった PR: PR 番号、title、URL、マージしなかった理由、人間確認が必要な点。
- upstream release notes / changelog / migration guide の確認結果。
- この repo で確認した影響範囲。
- post-merge action はないこと。

## PR コメント

マージしなかった Renovate PR にコメントを残すのは、次の条件に該当し、調査結果を PR 上に残す価値がある場合だけにする。

- upstream changelog に breaking change / migration / runtime requirement / configuration change の可能性がある。
- この repo の Go toolchain、GitHub Actions、release workflow、CLI runtime behavior への影響が否定できない。
- check failure や conflict 以外に、人間が判断すべき具体的な論点がある。

コメントには以下を含める。

- 確認した release notes / changelog / migration guide。
- この repo で影響を受ける可能性がある workflow / file / behavior。
- 自動マージしなかった具体的理由。
- 人間に確認してほしい点。

既に同等内容の comment がある場合は、重複コメントを投稿せず comment skipped として扱い、既存コメント URL を報告する。マージしなかったが人間確認が必要な Renovate PR には、コメント投稿の有無に関係なく `renovate-needs-manual-review` を付ける。既に同等内容の comment がある場合も、重複コメントは投稿せず label は付ける。

単に check が failed / pending、conflict、requested changes だけの場合は、コメントせず最終報告に理由を書く。

## 禁止操作

- Renovate branch に commit や push をしない。
- PR を close しない。
- `ding update` を実行しない。
- release tag を作らない。
- GitHub Release を作らない。
- local installed `ding` を更新しない。
- この skill に明記されていない条件の PR はマージしない。
