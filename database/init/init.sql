-- ─── ID Generator 数据库初始化 ────────────────────────────────────────────────
-- 号段模式（Leaf Segment）：每个业务标签独立维护号段，支持多业务线并发生成

CREATE DATABASE IF NOT EXISTS `idgen` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE `idgen`;

-- ============================================
-- 号段分配表 (leaf_alloc)
-- biz_tag: 业务标签（全局唯一）
-- max_id:  当前已分配的最大 ID（下一号段从 max_id+1 开始）
-- step:    每次分配的号段大小，可按业务量调整
-- ============================================
CREATE TABLE IF NOT EXISTS `leaf_alloc` (
    `biz_tag`     VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务标签（业务方唯一标识，如 order/user/coupon）',
    `max_id`      BIGINT       NOT NULL DEFAULT 1  COMMENT '当前已分配到的最大号段 ID',
    `step`        INT          NOT NULL             COMMENT '号段步长（每次从 DB 获取的 ID 数量）',
    `description` VARCHAR(256)          DEFAULT NULL COMMENT '业务描述',
    `update_time` DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '最后更新时间',
    PRIMARY KEY (`biz_tag`),
    INDEX `idx_update_time` (`update_time`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COMMENT = '号段分配表';

-- ─── 种子数据 ─────────────────────────────────────────────────────────────────
-- step 建议：低频业务 1000，中频业务 10000，高频业务 100000
INSERT INTO `leaf_alloc` (`biz_tag`, `max_id`, `step`, `description`)
VALUES
    ('order',    1000000,  10000, '订单 ID'),
    ('user',     1000000,  10000, '用户 ID'),
    ('coupon',   1000000,   1000, '优惠券 ID'),
    ('payment',  1000000,  10000, '支付单 ID'),
    ('account',  1000000,  10000, '账户 ID')
ON DUPLICATE KEY UPDATE `description` = VALUES(`description`);
