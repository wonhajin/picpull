# picpull

웹페이지 이미지를 화면 순서대로 저장하는 Go CLI. 설치된 Google Chrome을 chromedp로 띄운다. 사용법은 `README.md`.

## 명령

```sh
go build -o dist/picpull ./cmd/picpull
go vet ./... && gofmt -l .        # gofmt 출력이 없어야 한다
go test ./...
go test -race ./internal/page ./internal/app
```

- **브라우저 테스트는 Chrome이 없으면 조용히 SKIP된다** — `ok`만 보고 통과로 판단하지 않는다. Chrome을 못 찾는 환경이면 `PICPULL_TEST_BROWSER=<Chrome 실행 파일 경로>`를 주고, `go test -v`로 SKIP이 없는지 확인한다. CI는 경로를 강제해 SKIP 대신 실패하게 했다.

## 함정

- 사용자에게 보이는 문구(오류·경고·출력 줄)는 새로 넣거나 바꾸기 전에 사용자 승인을 받는다.
- `docs/`는 gitignore다. 설계 스펙(`docs/superpowers/specs/2026-10-09-picpull-design.md`)은 로컬에만 있다 — CLI 옵션·설정 파일·저장 구조를 바꿀 때 먼저 읽는다.
- `--next` 순회 중 이전 페이지 응답을 거르는 로직(`internal/page`의 `Reset`·cutoff·purge)은 Performance 도메인 시각에 의존한다. 고칠 때는 실브라우저 테스트(`TestResetReadsBrowserClock`, `TestCachedImageSharedByNextPageIsKept`)가 실제로 돌았는지 확인한다.

## 워크플로 (레포 고유)

- 릴리스는 `main` 머지가 아니라 **`v*` 태그 push**로 발동한다(`.github/workflows/release.yml` → GoReleaser → GitHub Release + `wonhajin/homebrew-tap` cask). 태그는 사용자가 만든다.
