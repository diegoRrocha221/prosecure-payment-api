package database

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "time"
    "prosecure-payment-api/models"
    "prosecure-payment-api/utils"
)

type DatabaseConfig struct {
    Host     string
    User     string
    Password string
    DBName   string
}

type Connection struct {
    db *sql.DB
}

func NewConnection(config DatabaseConfig) (*Connection, error) {
    dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true",
        config.User, config.Password, config.Host, config.DBName)
       
    db, err := sql.Open("mysql", dsn)
    if err != nil {
        return nil, fmt.Errorf("failed to connect to database: %v", err)
    }

    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(25)
    db.SetConnMaxLifetime(5 * time.Minute)
    db.SetConnMaxIdleTime(5 * time.Minute)

    conn := &Connection{db: db}

    if err := conn.ensureConnection(); err != nil {
        db.Close()
        return nil, err
    }

    return conn, nil
}

func (c *Connection) LockCheckout(checkoutID string) (bool, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    result, err := c.db.ExecContext(ctx, `
        INSERT INTO checkout_locks (checkout_id, locked_at)
        VALUES (?, NOW())
        ON DUPLICATE KEY UPDATE
        locked_at = IF(locked_at < NOW() - INTERVAL 5 MINUTE, NOW(), locked_at)
    `, checkoutID)
    
    if err != nil {
        return false, fmt.Errorf("error acquiring lock: %v", err)
    }
    
    rows, err := result.RowsAffected()
    if err != nil {
        return false, err
    }
    
    return rows > 0, nil
}

func (c *Connection) ReleaseLock(checkoutID string) error {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    _, err := c.db.ExecContext(ctx, `
        DELETE FROM checkout_locks
        WHERE checkout_id = ?
    `, checkoutID)
    
    if err != nil {
        return fmt.Errorf("error releasing lock: %v", err)
    }
    
    return nil
}
func (c *Connection) ensureConnection() error {
    for retries := 0; retries < 3; retries++ {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        err := c.db.PingContext(ctx)
        cancel()
        
        if err == nil {
            return nil
        }
        
        log.Printf("Database ping failed (attempt %d/3): %v", retries+1, err)
        time.Sleep(time.Second * time.Duration(retries+1))
    }
    return fmt.Errorf("failed to establish database connection after 3 attempts")
}

func (c *Connection) Close() error {
    return c.db.Close()
}

func (c *Connection) Ping() error {
    return c.ensureConnection()
}

func (c *Connection) GetDB() *sql.DB {
    return c.db
}

func (c *Connection) BeginTransaction() (*Transaction, error) {
    if err := c.ensureConnection(); err != nil {
        return nil, fmt.Errorf("database connection check failed: %v", err)
    }
    
    tx, err := c.db.Begin()
    if err != nil {
        return nil, fmt.Errorf("failed to begin transaction: %v", err)
    }
    return &Transaction{tx: tx}, nil
}

func (c *Connection) GetCheckoutData(checkoutID string) (*models.CheckoutData, error) {
	log.Printf("Fetching checkout data for ID: %s", checkoutID)
	
	query := `
			SELECT 
					ch.name, 
					ch.email, 
					ch.plan, 
					ch.zipcode, 
					ch.state, 
					ch.city, 
					ch.street, 
					ch.phoneNumber,
					ch.additional, 
					ch.plans_json, 
					ch.username, 
					ch.passphrase
			FROM checkout_historics ch
			WHERE ch.checkout_id = ?
	`
	
	var data models.CheckoutData
	var plansJSON string
	
	err := c.db.QueryRow(query, checkoutID).Scan(
			&data.Name,
			&data.Email,
			&data.PlanID,
			&data.ZipCode,
			&data.State,
			&data.City,
			&data.Street,
			&data.PhoneNumber,
			&data.Additional,
			&plansJSON,
			&data.Username,
			&data.Passphrase,
	)
	if err != nil {
			log.Printf("Error getting checkout data: %v", err)
			return nil, fmt.Errorf("error getting checkout data: %v", err)
	}

	var plansData []models.Plan
	if err := json.Unmarshal([]byte(plansJSON), &plansData); err != nil {
			log.Printf("Error parsing plans json: %v", err)
			return nil, fmt.Errorf("error parsing plans json: %v", err)
	}

	planQuantities := make(map[int]int)
	for _, plan := range plansData {
			planQuantities[plan.PlanID]++
	}

	totalItems := len(plansData)

	var plans []models.Plan
	for planID, quantity := range planQuantities {
			var planPrice float64
			var discountJSON string
			var planName string

			err := c.db.QueryRow(`
					SELECT name, price, single_discount 
					FROM plans 
					WHERE id = ?`, planID).Scan(&planName, &planPrice, &discountJSON)
			if err != nil {
					log.Printf("Error getting plan details for ID %d: %v", planID, err)
					continue
			}

			discountRules, err := utils.ParseDiscountRules(discountJSON)
			if err != nil {
					log.Printf("Warning: Error parsing discount rules for plan %d: %v", planID, err)
					discountRules = []utils.DiscountRule{}
			}

			discount, discountPercent := utils.CalculateDiscount(planPrice, totalItems, discountRules)
			finalPrice := utils.Round(planPrice - discount)

			for i := 0; i < quantity; i++ {
					plan := models.Plan{
							PlanID:    planID,
							PlanName:  planName,
							BasePrice: planPrice,
							Price:     finalPrice,
							Discount:  discountPercent,
							Username:  data.Username,
							Email:     data.Email,
							Quantity:  quantity,
					}
					plans = append(plans, plan)
			}
	}

	var subtotal float64
	var totalDiscount float64
	for _, plan := range plans {
			subtotal += plan.BasePrice
			totalDiscount += (plan.BasePrice - plan.Price)
	}

	data.ID = checkoutID
	data.PlansJSON = plansJSON
	data.Plans = plans
	data.Subtotal = utils.Round(subtotal)
	data.Discount = utils.Round(totalDiscount)
	data.Total = utils.Round(subtotal - totalDiscount)

	log.Printf("Successfully fetched checkout data with pricing: %+v", data)
	return &data, nil
}

func (c *Connection) UpdateUserActivationCode(email, username, code string) error {
    if err := c.ensureConnection(); err != nil {
        return fmt.Errorf("database connection check failed: %v", err)
    }

    log.Printf("Updating activation code for user: %s", username)
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    query := `UPDATE users SET confirmation_code = ? WHERE username = ? AND email = ?`
   
    result, err := c.db.ExecContext(ctx, query, code, username, email)
    if err != nil {
        log.Printf("Error updating activation code: %v", err)
        return fmt.Errorf("error updating activation code: %v", err)
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return fmt.Errorf("error getting rows affected: %v", err)
    }

    if rowsAffected == 0 {
        return fmt.Errorf("no user found with username %s and email %s", username, email)
    }

    log.Printf("Successfully updated activation code for user: %s", username)
    return nil
}

func (c *Connection) IsCheckoutProcessed(checkoutID string) (bool, error) {
	var exists bool
	query := `
			SELECT EXISTS(
					SELECT 1 
					FROM master_accounts ma 
					JOIN transactions t ON t.master_reference = ma.reference_uuid 
					WHERE t.checkout_id = ? AND t.status = 'authorized'
			)
	`
	
	err := c.db.QueryRow(query, checkoutID).Scan(&exists)
	if err != nil {
			return false, fmt.Errorf("error checking checkout status: %v", err)
	}
	
	return exists, nil
}
func (c *Connection) GetPlans() ([]models.PlanCart, error) {
	query := `
			SELECT id, image, name, description, price, rules, 
						 single_discount, discount_rule_applied 
			FROM plans 
			WHERE deleted_at IS NULL
			AND id IN (4, 5, 6, 7)  -- Apenas os planos ativos que queremos mostrar
			ORDER BY id ASC
	`
	
	rows, err := c.db.Query(query)
	if err != nil {
			return nil, err
	}
	defer rows.Close()

	var plans []models.PlanCart
	for rows.Next() {
			var plan models.PlanCart
			err := rows.Scan(
					&plan.ID,
					&plan.Image,
					&plan.Name,
					&plan.Description,
					&plan.Price,
					&plan.Rules,
					&plan.SingleDiscount,
					&plan.DiscountRuleApplied,
			)
			if err != nil {
					return nil, err
			}
			plans = append(plans, plan)
	}

	return plans, rows.Err()
}

func (c *Connection) GetPlanByID(id int) (*models.PlanCart, error) {
	query := `
			SELECT id, image, name, description, price, rules, 
						 single_discount, discount_rule_applied 
			FROM plans 
			WHERE id = ? AND deleted_at IS NULL
	`
	
	var plan models.PlanCart
	err := c.db.QueryRow(query, id).Scan(
			&plan.ID,
			&plan.Image,
			&plan.Name,
			&plan.Description,
			&plan.Price,
			&plan.Rules,
			&plan.SingleDiscount,
			&plan.DiscountRuleApplied,
	)
	if err != nil {
			return nil, err
	}
	return &plan, nil
}