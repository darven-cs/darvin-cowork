import { describe, expect, test, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import App from './App';

vi.mock('./api/hello', () => ({
  sayHello: vi.fn(),
}));

import { sayHello } from './api/hello';
const mockedSayHello = vi.mocked(sayHello);

describe('<App />', () => {
  test('renders the trigger button', () => {
    render(<App />);
    expect(screen.getByRole('button', { name: /点击/ })).toBeInTheDocument();
  });

  test('shows msg on successful click', async () => {
    mockedSayHello.mockResolvedValueOnce({ msg: '你好世界' });

    render(<App />);
    fireEvent.click(screen.getByRole('button', { name: /点击/ }));

    await waitFor(() => {
      expect(screen.getByText('你好世界')).toBeInTheDocument();
    });
  });

  test('shows error on failure', async () => {
    mockedSayHello.mockRejectedValueOnce(new Error('boom'));

    render(<App />);
    fireEvent.click(screen.getByRole('button', { name: /点击/ }));

    await waitFor(() => {
      expect(screen.getByText(/错误: boom/)).toBeInTheDocument();
    });
  });
});
