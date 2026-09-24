import { StyleProvider } from '@ant-design/cssinjs';
import { App as AntApp, ConfigProvider } from 'antd';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { RouterProvider } from 'react-router';

import './index.css';
import './monaco';

import { router } from './app/routes';
import { buildTheme } from './app/theme';
import { PanelProvider } from './data/store';

/**
 * The root: theme, panel store, then the router (`app/routes.tsx`). The entry
 * flow lives in the router — `app/RequireAuth.tsx` gates the shell and the
 * auth screens (`/signin`, `/setup`) stand outside it — so a deep link
 * survives signing in and signing out lands on `/signin` with the shell gone.
 */

function Root() {
  return (
    <StyleProvider layer>
      <ConfigProvider theme={buildTheme('light')}>
        <AntApp>
          <PanelProvider>
            <RouterProvider router={router} />
          </PanelProvider>
        </AntApp>
      </ConfigProvider>
    </StyleProvider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
);
