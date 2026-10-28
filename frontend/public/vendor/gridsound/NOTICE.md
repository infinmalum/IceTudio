# GridSound subset used by IceTudio

Source: https://github.com/gridsound/gs-ui-components
Commit: c45632a4b6fbc38af87a6e68058bdb9d2c444242
Submodule: https://github.com/gridsound/gs-utils
Commit: 2db8ea908624f3c6e8ad24333249a3fdb8fbcf5a

Copyright and license: upstream GridSound contributors; GNU AGPL version 3 (see LICENSE and gs-utils/LICENSE). Original notices remain in the copied files. manifest.json lists the exact shipped source subset. IceTudio's distribution includes these sources and must supply corresponding application source under the project AGPL-3.0-only direction.

Local modifications, 2026-10-17:
- gsui-gray.css: scope theme variables to .piano-host instead of the document body.
- gsuiBlocksManager.js and gsuiPianoroll.js: add a host cancellation method to remove active block gesture listeners without committing a cancelled gesture. IceTudio restores the authoritative projection afterward.

No GridSound assets, fonts, soundfonts or samples are included. The general application reset styles and synthesizer parameter editor are not adopted. MIDI velocity is edited by the IceTudio host. Local adapter code is in frontend/src/music and frontend/src/components/PianoRoll.tsx.

Local modifications, 2026-10-18:
- gsuiPianoroll.html.js and gsuiPianoroll.js: omit note-link dragline/drop DOM from each note and allow pointer handling without that widget. IceTudio does not expose the upstream synthesizer's linked-note contract; its MIDI note model and existing move/resize/selection controls are retained. Empty dragline messages remain no-ops in upstream helpers. This avoids allocating hidden link widgets for dense MIDI clips.

Local modification, 2026-10-27:
- gsuiSliderGroup.js: read the numeric z-index already set inline by the slider instead of repeatedly requesting computed style during selection changes. This avoids repeated style recalculation for large selections while retaining the existing stacking adjustment.
