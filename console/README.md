# TourDesk Console (M7)
React+TS+Vite console over the Go backend `/queue/*` APIs.
- Dev: `npm install && npm run dev` (proxies /queue, /analytics to :8080).
- Auth is dev-grade: paste/generate an HS256 bearer + tenant id in the session bar (decision ①).
- Queue updates by polling every 5s (decision ②). Draft editor is plain-text (decision ③).
- Production SSO redirect, real-time push, and rich-text are later slices.
Test: `npm test`. Typecheck: `npm run typecheck`.
