import type { FormEvent } from "react";
import { ArrowRight, Clapperboard } from "lucide-react";

type Props = {
  password: string;
  message: string;
  setPassword: (value: string) => void;
  submit: (event: FormEvent<HTMLFormElement>) => void;
};

export function LoginPage({ password, message, setPassword, submit }: Props) {
  return <main className="login-page">
    <form className="login-card" onSubmit={submit}>
      <span className="login-brand"><Clapperboard size={25} strokeWidth={1.8} aria-hidden="true" /> 创作工作室</span>
      <span className="eyebrow">VIDEO PRODUCTION CONSOLE</span>
      <h1>视频生产控制台</h1>
      <p>文案、图文与短片，一个有序的创作空间。<br />请输入管理口令后继续。</p>
      {message && <div className="notice" role="alert">{message}</div>}
      <label htmlFor="admin-password">管理口令</label>
      <input id="admin-password" autoFocus type="password" value={password}
        onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" />
      <button type="submit">进入控制台 <ArrowRight size={17} aria-hidden="true" /></button>
    </form>
  </main>;
}
