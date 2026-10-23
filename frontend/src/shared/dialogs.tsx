import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
type Question = {
  title: string;
  body?: ReactNode;
  label?: string;
  initial?: string;
  confirm?: string;
  cancel?: string;
  danger?: boolean;
  // Names and other single-value fields use a one-line input so Enter
  // submits; multi-line notes keep the textarea.
  singleLine?: boolean;
  // Two equally valid actions (e.g. edit the existing assistant or create
  // another): each button resolves with its value, 取消/Esc resolves null.
  choices?: { label: string; value: string }[];
};
type Request = { question: Question; resolve: (value: string | null) => void };
type Ask = ((q: Question) => Promise<string | null>) & { hasPending: () => boolean };
const DialogContext = createContext<Ask>(Object.assign(() => Promise.resolve(null), { hasPending: () => false }));
export function DialogProvider({ children }: { children: ReactNode }) {
  const [request, setRequest] = useState<Request | null>(null);
  const queue = useRef<Request[]>([]);
  const current = useRef<Request | null>(null);
  const [value, setValue] = useState("");
  const box = useRef<HTMLDialogElement>(null);
  const ask = Object.assign((question: Question) =>
    new Promise<string | null>((resolve) => {
      const next = { question, resolve };
      if (current.current) queue.current.push(next);
      else {
        current.current = next;
        setRequest(next);
        setValue(question.initial ?? "");
      }
    }), { hasPending: () => current.current !== null });
  const finish = (value: string | null) => {
    current.current?.resolve(value);
    const next = queue.current.shift() ?? null;
    current.current = next;
    setRequest(next);
    setValue(next?.question.initial ?? "");
  };
  useEffect(() => {
    if (request) {
      const focus = document.activeElement as HTMLElement | null;
      box.current?.showModal();
      return () => {
        box.current?.close();
        focus?.focus();
      };
    }
  }, [request]);
  return (
    <DialogContext.Provider value={ask}>
      {children}
      {request && (
        <dialog
          ref={box}
          className="app-dialog"
          aria-labelledby="dialog-title"
          onCancel={(e) => {
            e.preventDefault();
            finish(null);
          }}
        >
          <form
            onSubmit={(e) => {
              e.preventDefault();
              finish(value);
            }}
          >
            <h2 id="dialog-title">{request.question.title}</h2>
            <div className="dialog-body">{request.question.body}</div>
            {request.question.label &&
              (request.question.singleLine ? (
                <label>
                  {request.question.label}
                  <input
                    autoFocus
                    type="text"
                    value={value}
                    onChange={(e) => setValue(e.target.value)}
                  />
                </label>
              ) : (
                <label>
                  {request.question.label}
                  <textarea
                    autoFocus
                    value={value}
                    onChange={(e) => setValue(e.target.value)}
                    rows={3}
                  />
                </label>
              ))}
            <div className="actions">
              <button
                type="button"
                className="secondary"
                onClick={() => finish(null)}
              >
                {request.question.cancel ?? "取消"}
              </button>
              {request.question.choices ? (
                request.question.choices.map((choice, index) => (
                  <button
                    key={choice.value}
                    type="button"
                    autoFocus={index === 0 && !request.question.label}
                    onClick={() => finish(choice.value)}
                  >
                    {choice.label}
                  </button>
                ))
              ) : (
                <button
                  autoFocus={!request.question.label}
                  className={request.question.danger ? "danger" : ""}
                  type="submit"
                >
                  {request.question.confirm ?? "确认"}
                </button>
              )}
            </div>
          </form>
        </dialog>
      )}
    </DialogContext.Provider>
  );
}
export function useDialog() {
  const ask = useContext(DialogContext);
  return { ask, confirm: async (q: Question) => (await ask(q)) !== null };
}
