-- Rollback of 530_rewards.sql. The coin ledger, the wallet and the purchases are dropped.
DROP TABLE IF EXISTS shop_office;
DROP TABLE IF EXISTS shop_equipment;
DROP TABLE IF EXISTS shop_inventory;
DROP TABLE IF EXISTS coin_ledger;
DROP TABLE IF EXISTS coin_wallets;
