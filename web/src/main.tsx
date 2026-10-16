import '@mantine/core/styles.css';
import '@mantine/dates/styles.css';
import '@mantine/charts/styles.css';
import '@mantine/notifications/styles.css';

import React from 'react';
import ReactDOM from 'react-dom/client';
import dayjs from 'dayjs';
import 'dayjs/locale/ru';
import { MantineProvider, createTheme } from '@mantine/core';
import { DatesProvider } from '@mantine/dates';
import { ModalsProvider } from '@mantine/modals';
import { Notifications } from '@mantine/notifications';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App } from './App';

dayjs.locale('ru');

const theme = createTheme({
  primaryColor: 'teal',
  defaultRadius: 'md',
  fontFamily: 'Inter, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
});

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 10_000, refetchOnWindowFocus: true, retry: 1 } },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <MantineProvider theme={theme} defaultColorScheme="auto">
        <DatesProvider settings={{ locale: 'ru', firstDayOfWeek: 1 }}>
          <ModalsProvider labels={{ confirm: 'Удалить', cancel: 'Отмена' }}>
            <Notifications position="top-right" />
            <App />
          </ModalsProvider>
        </DatesProvider>
      </MantineProvider>
    </QueryClientProvider>
  </React.StrictMode>,
);
