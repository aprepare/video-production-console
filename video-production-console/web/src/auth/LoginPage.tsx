import type { FormEvent } from "react";

type Props = {
  password: string;
  message: string;
  setPassword: (value: string) => void;
  submit: (event: FormEvent<HTMLFormElement>) => void;
};

export function LoginPage({ password, message, setPassword, submit }: Props) {
  return <main className="login-page">
    <form className="login-card" onSubmit={submit}>
      <span className="eyebrow">本机视频工作台</span>
      <h1>视频生产控制台</h1>
      <p>请输入管理口令后继续。</p>
      {message && <div className="notice" role="alert">{message}</div>}
      <label htmlFor="admin-password">管理口令</label>
      <input id="admin-password" autoFocus type="password" value={password}
        onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" />
      <button type="submit">进入控制台</button>
    </form>
  </main>;
}
