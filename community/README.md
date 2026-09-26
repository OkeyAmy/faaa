# Community sounds

Each `*.json` file here is a list of sounds merged into `index.json` by the
daily scraper. Put the audio file in `community/sounds/` (mp3/wav, ≤15s,
≤500KB) and reference it by raw URL:

```json
[
  {
    "id": "c-my-cool-sound",
    "title": "My cool sound",
    "artist": "your-github-handle",
    "url": "https://raw.githubusercontent.com/okeyamy/faaa/main/community/sounds/my-cool-sound.mp3",
    "duration": 3.2,
    "tags": ["meme", "funny"]
  }
]
```

Only submit sounds you made or have the right to share.
