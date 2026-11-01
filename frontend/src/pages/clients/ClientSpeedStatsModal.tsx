import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Modal, Select, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { z } from 'zod';

import { Sparkline } from '@/components/viz';
import { formatSpeed, type SpeedUnit } from '@/lib/clients/speedUnit';
import {
  ClientSpeedDaySchema,
  ClientSpeedSeriesSchema,
  type ClientSpeedDay,
  type ClientSpeedSeries,
} from '@/schemas/client';
import { HttpUtil } from '@/utils';

function utcToday(): string {
  return new Date().toISOString().slice(0, 10);
}

function labelFor(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`;
}

const emptySeries: ClientSpeedSeries = { email: '', day: '', points: [] };

async function fetchSpeedDays(day: string): Promise<ClientSpeedDay[]> {
  const msg = await HttpUtil.get('/panel/api/clients/speedStats', { day }, { silent: true });
  const parsed = z.array(ClientSpeedDaySchema).safeParse(msg.obj);
  return parsed.success ? parsed.data : [];
}

async function fetchSpeedSeries(email: string, day: string): Promise<ClientSpeedSeries> {
  const msg = await HttpUtil.get(
    `/panel/api/clients/speedStats/${encodeURIComponent(email)}`,
    { day },
    { silent: true },
  );
  const parsed = ClientSpeedSeriesSchema.safeParse(msg.obj);
  return parsed.success ? parsed.data : emptySeries;
}

interface ClientSpeedStatsModalProps {
  open: boolean;
  onClose: () => void;
}

export default function ClientSpeedStatsModal({ open, onClose }: ClientSpeedStatsModalProps) {
  const { t } = useTranslation();
  const [day, setDay] = useState(utcToday);
  const [email, setEmail] = useState<string | null>(null);
  const [unit, setUnit] = useState<SpeedUnit>('kb');

  const days = useQuery({
    queryKey: ['clients', 'speedStats', day],
    enabled: open,
    queryFn: () => fetchSpeedDays(day),
  });
  const series = useQuery({
    queryKey: ['clients', 'speedSeries', email, day],
    enabled: open && email != null && email !== '',
    queryFn: () => fetchSpeedSeries(email ?? '', day),
  });

  const rows = days.data ?? [];
  const points = series.data?.points ?? [];
  const unitLabel = unit === 'mb' ? t('pages.clients.speedUnitMb') : t('pages.clients.speedUnitKb');
  const columns: ColumnsType<ClientSpeedDay> = [
    { title: t('pages.clients.email'), dataIndex: 'email', key: 'email' },
    {
      title: `${t('pages.clients.speedDownload')} ${t('pages.clients.speedMax')}, ${unitLabel}`,
      dataIndex: 'maxDownKBs',
      render: (value: number) => formatSpeed(value, unit),
    },
    {
      title: `${t('pages.clients.speedDownload')} ${t('pages.clients.speedAvg')}, ${unitLabel}`,
      dataIndex: 'avgDownKBs',
      render: (value: number) => formatSpeed(value, unit),
    },
    {
      title: `${t('pages.clients.speedUpload')} ${t('pages.clients.speedMax')}, ${unitLabel}`,
      dataIndex: 'maxUpKBs',
      render: (value: number) => formatSpeed(value, unit),
    },
    {
      title: `${t('pages.clients.speedUpload')} ${t('pages.clients.speedAvg')}, ${unitLabel}`,
      dataIndex: 'avgUpKBs',
      render: (value: number) => formatSpeed(value, unit),
    },
  ];

  return (
    <Modal
      open={open}
      title={t('pages.clients.speedStatsTitle')}
      onCancel={onClose}
      footer={null}
      width={960}
      destroyOnHidden
    >
      <div style={{ display: 'flex', gap: 12, alignItems: 'center', marginBottom: 12 }}>
        <label htmlFor="client-speed-day">{t('pages.clients.speedDay')}</label>
        <input
          id="client-speed-day"
          type="date"
          value={day}
          onChange={(event) => {
            setDay(event.target.value || utcToday());
            setEmail(null);
          }}
        />
        <Select<SpeedUnit>
          aria-label={t('pages.clients.speedUnitKb')}
          value={unit}
          popupMatchSelectWidth={false}
          options={[
            { value: 'kb', label: t('pages.clients.speedUnitKb') },
            { value: 'mb', label: t('pages.clients.speedUnitMb') },
          ]}
          onChange={setUnit}
        />
      </div>
      <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
        {t('pages.clients.speedUtcNote')}
      </Typography.Paragraph>
      <Table<ClientSpeedDay>
        size="small"
        rowKey="email"
        loading={days.isFetching}
        columns={columns}
        dataSource={rows}
        pagination={{ pageSize: 8, hideOnSinglePage: true }}
        locale={{ emptyText: t('pages.clients.speedEmpty') }}
        onRow={(row) => ({
          onClick: () => setEmail(row.email),
          style: { cursor: 'pointer' },
        })}
      />
      <Typography.Title level={5} style={{ marginTop: 16 }}>
        {email ? `${t('pages.clients.speedChart')}: ${email}` : t('pages.clients.speedChart')}
      </Typography.Title>
      {email && points.length > 0 ? (
        <Sparkline
          data={points.map((point) => (unit === 'mb' ? point.downKBs / 1024 : point.downKBs))}
          data2={points.map((point) => (unit === 'mb' ? point.upKBs / 1024 : point.upKBs))}
          labels={points.map((point) => labelFor(point.sec))}
          name1={`${t('pages.clients.speedDownload')} (${unit === 'mb' ? t('pages.clients.speedUnitMb') : t('pages.clients.speedUnitKb')})`}
          name2={`${t('pages.clients.speedUpload')} (${unit === 'mb' ? t('pages.clients.speedUnitMb') : t('pages.clients.speedUnitKb')})`}
          height={220}
          maxPoints={288}
          showAxes
          showLegend
          showGrid
          valueMin={0}
          valueMax={null}
          yFormatter={(value) => formatSpeed(value, 'kb')}
        />
      ) : (
        <Typography.Text type="secondary">{t('pages.clients.speedPickClient')}</Typography.Text>
      )}
    </Modal>
  );
}
