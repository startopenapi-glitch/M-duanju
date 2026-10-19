# License notes

The source code, documentation and original generated diagrams in this
repository are licensed under Apache-2.0.

Third-party dependencies keep their own licenses:

- Go modules: see `backend/go.mod` / `backend/go.sum`.
- npm packages: see `frontend/package.json` / `frontend/package-lock.json`.
- ffmpeg/ffprobe: **not included**. Install them separately. If you redistribute
  a container image containing ffmpeg, comply with the applicable GPL/LGPL terms.
- Any images, fonts or sample media added later must be credited and licensed
  separately; do not assume Apache-2.0 covers them.

No production database dumps, payment credentials, certificates or user data
are included in this repository.
