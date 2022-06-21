// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package testdata

import "storj.io/storj/storagenode/storagenodedb"

var v8 = MultiDBState{
	Version: 8,
	DBStates: DBStates{
		storagenodedb.DeprecatedInfoDBName: &DBState{
			SQL: `
				-- table for keeping serials that need to be verified against
				CREATE TABLE used_serial (
					satellite_id  BLOB NOT NULL,
					serial_number BLOB NOT NULL,
					expiration    TIMESTAMP NOT NULL
				);
				-- primary key on satellite id and serial number
				CREATE UNIQUE INDEX pk_used_serial ON used_serial(satellite_id, serial_number);
				-- expiration index to allow fast deletion
				CREATE INDEX idx_used_serial ON used_serial(expiration);

				-- certificate table for storing uplink/satellite certificates
				CREATE TABLE certificate (
					cert_id       INTEGER
				);

				-- table for storing piece meta info
				CREATE TABLE pieceinfo (
					satellite_id     BLOB      NOT NULL,
					piece_id         BLOB      NOT NULL,
					piece_size       BIGINT    NOT NULL,
					piece_expiration TIMESTAMP,

					uplink_piece_hash BLOB    NOT NULL,
					uplink_cert_id    INTEGER NOT NULL,

					deletion_failed_at TIMESTAMP,
					piece_creation TIMESTAMP NOT NULL,

					FOREIGN KEY(uplink_cert_id) REFERENCES certificate(cert_id)
				);
				-- primary key by satellite id and piece id
				CREATE UNIQUE INDEX pk_pieceinfo ON pieceinfo(satellite_id, piece_id);

				-- table for storing bandwidth usage
				CREATE TABLE bandwidth_usage (
					satellite_id  BLOB    NOT NULL,
					action        INTEGER NOT NULL,
					amount        BIGINT  NOT NULL,
					created_at    TIMESTAMP NOT NULL
				);
				CREATE INDEX idx_bandwidth_usage_satellite ON bandwidth_usage(satellite_id);
				CREATE INDEX idx_bandwidth_usage_created   ON bandwidth_usage(created_at);

				-- table for storing all unsent orders
				CREATE TABLE unsent_order (
					satellite_id  BLOB NOT NULL,
					serial_number BLOB NOT NULL,

					order_limit_serialized BLOB      NOT NULL,
					order_serialized       BLOB      NOT NULL,
					order_limit_expiration TIMESTAMP NOT NULL,

					uplink_cert_id INTEGER NOT NULL,

					FOREIGN KEY(uplink_cert_id) REFERENCES certificate(cert_id)
				);
				CREATE UNIQUE INDEX idx_orders ON unsent_order(satellite_id, serial_number);

				-- table for storing all sent orders
				CREATE TABLE order_archive (
					satellite_id  BLOB NOT NULL,
					serial_number BLOB NOT NULL,

					order_limit_serialized BLOB NOT NULL,
					order_serialized       BLOB NOT NULL,

					uplink_cert_id INTEGER NOT NULL,

					status      INTEGER   NOT NULL,
					archived_at TIMESTAMP NOT NULL,

					FOREIGN KEY(uplink_cert_id) REFERENCES certificate(cert_id)
				);

				CREATE INDEX idx_order_archive_satellite ON order_archive(satellite_id);
				CREATE INDEX idx_order_archive_status ON order_archive(status);

				-- table for storing vouchers
				CREATE TABLE vouchers (
					satellite_id BLOB PRIMARY KEY NOT NULL,
					voucher_serialized BLOB NOT NULL,
					expiration TIMESTAMP NOT NULL
				);

				INSERT INTO unsent_order VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',X'1eddef484b4c03f01332279032796972',X'0a101eddef484b4c03f0133227903279697212202b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf410001a201968996e7ef170a402fdfd88b6753df792c063c07c555905ffac9cd3cbd1c00022200ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac30002a20d00cf14f3c68b56321ace04902dec0484eb6f9098b22b31c6b3f82db249f191630643802420c08dfeb88e50510a8c1a5b9034a0c08dfeb88e50510a8c1a5b9035246304402204df59dc6f5d1bb7217105efbc9b3604d19189af37a81efbf16258e5d7db5549e02203bb4ead16e6e7f10f658558c22b59c3339911841e8dbaae6e2dea821f7326894',X'0a101eddef484b4c03f0133227903279697210321a47304502206d4c106ddec88140414bac5979c95bdea7de2e0ecc5be766e08f7d5ea36641a7022100e932ff858f15885ffa52d07e260c2c25d3861810ea6157956c1793ad0c906284','2019-04-01 16:01:35.9254586+00:00',1);

				INSERT INTO pieceinfo VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',X'd5e757fd8d207d1c46583fb58330f803dc961b71147308ff75ff1e72a0df6b0b',123,'2019-05-09 00:00:00.000000+00:00',X'0a20d5e757fd8d207d1c46583fb58330f803dc961b71147308ff75ff1e72a0df6b0b120501020304051a47304502201c16d76ecd9b208f7ad9f1edf66ce73dce50da6bde6bbd7d278415099a727421022100ca730450e7f6506c2647516f6e20d0641e47c8270f58dde2bb07d1f5a3a45673',1,NULL,'epoch');
				INSERT INTO pieceinfo VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',X'd5e757fd8d207d1c46583fb58330f803dc961b71147308ff75ff1e72a0df6b0b',123,'2019-05-09 00:00:00.000000+00:00',X'0a20d5e757fd8d207d1c46583fb58330f803dc961b71147308ff75ff1e72a0df6b0b120501020304051a483046022100e623cf4705046e2c04d5b42d5edbecb81f000459713ad460c691b3361817adbf022100993da2a5298bb88de6c35b2e54009d1bf306cda5d441c228aa9eaf981ceb0f3d',2,NULL,'epoch');

				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',0,0,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',0,0,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',1,1,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',1,1,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',2,2,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',2,2,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',3,3,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',3,3,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',4,4,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',4,4,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',5,5,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',5,5,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',6,6,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',6,6,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',1,1,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',1,1,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',2,2,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',2,2,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',3,3,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',3,3,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',4,4,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',4,4,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',5,5,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',5,5,'2019-04-01 20:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'0ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac3000',6,6,'2019-04-01 18:51:24.1074772+00:00');
				INSERT INTO bandwidth_usage VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',6,6,'2019-04-01 20:51:24.1074772+00:00');

				INSERT INTO order_archive VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000',X'62180593328b8ff3c9f97565fdfd305d',X'0a1062180593328b8ff3c9f97565fdfd305d12202b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf410001a201968996e7ef170a402fdfd88b6753df792c063c07c555905ffac9cd3cbd1c00022200ed28abb2813e184a1e98b0f6605c4911ea468c7e8433eb583e0fca7ceac30002a2077003db64dfd50c5bdc84daf28bcef97f140d302c3e5bfd002bcc7ac04e1273430643802420c08fce688e50510a0ffe7ff014a0c08fce688e50510a0ffe7ff0152473045022100943d90068a1b1e6879b16a6ed8cdf0237005de09f61cddab884933fefd9692bf0220417a74f2e59523d962e800a1b06618f0113039d584e28aae37737e4a71555966',X'0a1062180593328b8ff3c9f97565fdfd305d10321a47304502200f4d97f03ad2d87501f68bfcf0525ec518aebf817cf56aa5eeaea53d01b153a102210096e60cf4b594837b43b5c841d283e4b72c9a09207d64bdd4665c700dc2e0a4a2',1,1,'2019-04-01 18:51:24.5374893+00:00');

				INSERT INTO vouchers VALUES(X'2b3a5863a41f25408a8f5348839d7a1361dbd886d75786bb139a8ca0bdf41000', X'd5e757fd8d207d1c46583fb58330f803dc961b71147308ff75ff1e72a0df6b0b', '2019-07-04 00:00:00.000000+00:00');

				CREATE INDEX idx_pieceinfo_expiration ON pieceinfo(piece_expiration);
			`,
			NewData: `
				INSERT INTO used_serial VALUES(X'0693a8529105f5ff763e30b6f58ead3fe7a4f93f32b4b298073c01b2b39fa76e',X'18283dd3cec0a5abf6112e903549bdff','2019-04-01 18:58:53.3169599+00:00');
				INSERT INTO used_serial VALUES(X'976a6bbcfcec9d96d847f8642c377d5f23c118187fb0ca21e9e1c5a9fbafa5f7',X'18283dd3cec0a5abf6112e903549bdff','2019-04-01 18:58:53.3169599+00:00');
			`,
		},
	},
}
