"use strict";

$.$setTemplate( "gsui-pianoroll-block", () =>
	$.$div( { class: "gsuiBlocksManager-block gsuiPianoroll-block", "data-action": "move" },
		$.$span(),
		$.$div( { "data-action": "cropB" } ),
	)
);
