import { useState } from 'react';
import { sayHello } from './api/hello';
import './App.css';

function App() {
  const [msg, setMsg] = useState('');
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');

  async function handleHello() {
    setLoading(true);
    setErr('');
    try {
      const data = await sayHello();
      setMsg(data.msg);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setLoading(false);
    }
  }

  return (
    <div>
      <button
        onClick={handleHello}
        disabled={loading}
        style={{ padding: '8px 16px', fontSize: 16 }}
      >
        {loading ? '请求中...' : '点击'}
      </button>

      {msg && <p>{msg}</p>}
      {err && <p style={{ color: 'red' }}>错误: {err}</p>}
    </div>
  );
}

export default App;
