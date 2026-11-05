# Screenshots

The README's screenshots in `docs/images` are the real settings pages of this
checkout, filled with sample data. To take them again after a change to the
pages:

```powershell
.\docs\screenshots\make.ps1                      # writes docs/images
.\docs\screenshots\make.ps1 -Out $env:TEMP\shots # or look first
```

It needs voice running (for the shape of its settings), Python 3, Node 22 or
newer and Microsoft Edge.

- `build.py` copies the pages and gives them `mock.js`, which answers their API
  calls. From the live settings it keeps only the shape: the sessions,
  characters, transcript, devices and voices are sample data. It stops if
  anything the live data says about this PC (session names, projects, character
  names, cloned voices, devices, folders) would be shown.
- `mock.js` holds the sample: Anna speaking in `shop-api`, Clara and Rebekka in
  `notes-app` and `website`, a scene in the story theme for the reading window.
  `?shot=` on a page opens what a screenshot shows beyond its first view.
- `capture.mjs` drives Edge over its DevTools protocol, which sets an exact
  viewport at 2x scale.

The reading window's story theme comes from the role-play package or the
installed `storybook` theme; without it, that screenshot shows the default look.
