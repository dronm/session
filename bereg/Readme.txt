                                  Таблица "public.sessions"
   Столбец   |           Тип            | Правило сортировки | Допустимость NULL | По умолчанию 
-------------+--------------------------+--------------------+-------------------+--------------
 id          | character(128)           |                    | not null          | 
 set_time    | timestamp with time zone |                    |                   | now()
 data        | text                     |                    |                   | 
 session_key | character(128)           |                    |                   | 
 pub_key     | character varying(15)    |                    |                   | 
 data_enc    | bytea                    |                    |                   | 
 create_time | timestamp with time zone |                    |                   | now()
Индексы:
    "sessions_pub_key_index" btree (pub_key)
    "sessions_set_time_idx" btree (set_time)
Триггеры:
    sessions_trigger_after AFTER DELETE ON sessions FOR EACH ROW EXECUTE FUNCTION sessions_process()

SELECT sess_enc_write('%s','%s','%s','%s',%d)
FUNCTION sess_enc_write(
    in_id character varying,
    in_data_enc text,
    in_key text,
    in_remote_ip character varying)
  RETURNS void AS



SELECT sess_enc_read('%s','%s')
sess_enc_read(in_id character varying,in_key text)

SELECT sess_gc('%d seconds'::interval)
