// Classic scripts share upstream lexical globals. Load exactly once, in order,
// from the shipped pinned subset; no CDN, external assets or model connection.
let loading: Promise<void> | undefined;
export function loadGridSound() {
  return loading ??= (async () => {
    const base = `${import.meta.env.BASE_URL}vendor/gridsound/`;
    const response = await fetch(`${base}manifest.json`);
    if (!response.ok) throw Error("钢琴卷帘资源加载失败，请重新打开应用。");
    const manifest: { styles: string[]; scripts: string[] } = await response.json();
    await Promise.all(manifest.styles.map(path => new Promise<void>((resolve, reject) => {
      const link = document.createElement("link");
      link.rel = "stylesheet";
      link.href = base + path;
      link.onload = () => resolve();
      link.onerror = () => reject(Error(`无法加载钢琴卷帘样式 ${path}`));
      document.head.append(link);
    })));
    for (const path of manifest.scripts) {
      await new Promise<void>((resolve, reject) => {
        const script = document.createElement("script");
        script.src = base + path;
        script.onload = () => resolve();
        script.onerror = () => reject(Error(`无法加载钢琴卷帘组件 ${path}`));
        document.head.append(script);
      });
    }
    const Roll = customElements.get("gsui-pianoroll") as CustomElementConstructor & { $keyNotation(name: string): void };
    Roll.$keyNotation("CDEFGAB");
  })();
}
export interface GridNote {
  key: number; when: number; duration: number; gain: number; pan: number;
  lowpass: number; highpass: number; selected: boolean;
}
export interface GridElement extends HTMLElement {
  $octaves(from: number, count: number): void;
  $changeDuration(beats: number): void;
  $setData(data: Record<string, GridNote>): void;
  $setCallbacks(cb: (kind: string, ...args: any[]) => void): void;
  $timedivision(value: string): void;
  $addKey(id: string, value: GridNote): void;
  $removeKey(id: string): void;
  $changeKeyProp(id: string, prop: string, value: unknown): void;
  $scrollToKeys(): void;
  $cancel(): void;
}
